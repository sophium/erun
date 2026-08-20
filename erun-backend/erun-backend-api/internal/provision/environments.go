package provision

import (
	"context"
	"fmt"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	eruncommon "github.com/sophium/erun/erun-common"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/deployexec"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// EnvProvisionInput is the durable workflow input DBOS checkpoints: tenant
// identity plus the env's non-secret deploy coordinates. No secret is carried —
// the deploy Job runs under its own cluster-admin ServiceAccount.
type EnvProvisionInput struct {
	TenantID      string `json:"tenantId"`
	TenantType    string `json:"tenantType"`
	ErunUserID    string `json:"erunUserId,omitempty"`
	EnvironmentID string `json:"environmentId"`
	Tenant        string `json:"tenant"`
	Environment   string `json:"environment"`
	Version       string `json:"version"`
	// DeployID identifies one explicit deploy attempt. Being part of the
	// checkpointed input is what lets a resumed workflow rebuild the same Job
	// name and re-watch its own run. Empty on the create path, which deploys an
	// environment exactly once.
	DeployID string `json:"deployId,omitempty"`
	// MaxCPUMillicores/MaxMemoryMB/MaxStorageGB are the caller's tenant_quotas
	// row at request time, threaded into the deploy Job as --max-cpu/
	// --max-memory/--max-storage so the namespace gets a real ResourceQuota +
	// LimitRange (#605). Zero on all three (should not happen: routes always
	// populates them from TenantQuotaRepository.Get, which defaults an absent
	// row) skips the flags entirely, matching the pre-existing plain command.
	MaxCPUMillicores int `json:"maxCpuMillicores,omitempty"`
	MaxMemoryMB      int `json:"maxMemoryMb,omitempty"`
	MaxStorageGB     int `json:"maxStorageGb,omitempty"`
	// Bootstrap is decided once, synchronously, before the durable workflow is
	// enqueued (resolveBootstrapImage), and checkpointed here so a resumed
	// workflow does not re-probe the registry: true means the tenant's own
	// <tenant>-devops image was confirmed missing at Start/StartDeploy time, so
	// the deploy Job installs the canonical published erun-devops image+chart by
	// reference instead — the same bootstrap `erun deploy --runtime-image`
	// already supports for an operator's own machine.
	Bootstrap bool `json:"bootstrap,omitempty"`
}

// EnvCoordinator runs an env deploy to a terminal status — the service
// EnvironmentProvisioner (mark provisioning → run the Job → mark running/failed).
type EnvCoordinator interface {
	Provision(ctx context.Context, environmentID string, params deployexec.DeployJobParams) error
}

// EnvDeployConfig is the per-instance placement the deploy Job needs: the image
// registry, the namespace the Job runs in (the platform namespace), and the
// cluster-admin ServiceAccount it runs as.
type EnvDeployConfig struct {
	Registry               string
	PlatformNamespace      string
	DeployerServiceAccount string
	// ExposeTargetIP is the platform's ingress IP, threaded to every deploy Job
	// as DeployJobParams.ExposeTargetIP. Empty (the default) leaves env deploys
	// exactly as they were before #605's automatic exposure: this field, unlike
	// the three above, does not gate the executor itself (newEnvironmentProvisioner
	// stays enabled without it) — it only decides whether a deploy also chains
	// an expose.
	ExposeTargetIP string
}

// EnvProvisioner runs the durable env-deploy workflow, so a control-plane restart
// resumes it rather than re-running a completed deploy. It wraps the coordinator
// in a DBOS workflow keyed by environment id, so a retried create never
// double-deploys.
type EnvProvisioner struct {
	dbosCtx      dbos.DBOSContext
	coordinator  EnvCoordinator
	config       EnvDeployConfig
	imageChecker RuntimeImageChecker
	workflowFn   func(dbos.DBOSContext, EnvProvisionInput) (string, error)
}

// NewEnvProvisioner wires the durable provisioner. imageChecker may be nil,
// which skips the published-image precondition (Start/StartDeploy behave
// exactly as before it existed).
func NewEnvProvisioner(dbosCtx dbos.DBOSContext, coordinator EnvCoordinator, config EnvDeployConfig, imageChecker RuntimeImageChecker) *EnvProvisioner {
	p := &EnvProvisioner{dbosCtx: dbosCtx, coordinator: coordinator, config: config, imageChecker: imageChecker}
	// One stable function value shared by RegisterWorkflow and RunWorkflow, which
	// is how DBOS names the workflow and recovers it across restarts.
	p.workflowFn = p.provisionWorkflow
	dbos.RegisterWorkflow(dbosCtx, p.workflowFn)
	return p
}

// Start kicks off provisioning asynchronously so the HTTP handler returns
// immediately while the durable workflow runs the deploy. The environment id is
// the idempotency key, so a retried create does not start a second deploy.
func (p *EnvProvisioner) Start(input EnvProvisionInput) error {
	input.Bootstrap = p.resolveBootstrapImage(input)
	_, err := dbos.RunWorkflow(p.dbosCtx, p.workflowFn, input, dbos.WithWorkflowID("provision-env-"+input.EnvironmentID))
	return err
}

// StartDeploy runs the same durable workflow for an explicit deploy of an
// already-registered environment. It is keyed by the attempt rather than the
// environment: an environment-keyed id is terminal after the first deploy, which
// would make a retry after a failure — or a re-deploy at another version — a
// silent no-op. Concurrency is guarded by the environment's deploy claim, not by
// the workflow id.
func (p *EnvProvisioner) StartDeploy(input EnvProvisionInput) error {
	input.Bootstrap = p.resolveBootstrapImage(input)
	_, err := dbos.RunWorkflow(p.dbosCtx, p.workflowFn, input, dbos.WithWorkflowID("deploy-env-"+input.EnvironmentID+"-"+input.DeployID))
	return err
}

// resolveBootstrapImage runs the synchronous, best-effort precondition before
// enqueueing the durable workflow: whether this deploy must bootstrap on the
// canonical erun-devops image because the tenant's own <tenant>-devops image is
// confirmed missing from the registry — a control-plane tenant with no
// project has never run `erun push`, so its own image can never exist. A nil
// checker (not wired) or an inconclusive probe leaves Bootstrap false, matching
// RuntimeImageChecker's fail-open contract: only a registry-confirmed absence
// selects the fallback, never a network hiccup or an unreachable host.
func (p *EnvProvisioner) resolveBootstrapImage(input EnvProvisionInput) bool {
	if p.imageChecker == nil {
		return false
	}
	exists, err := p.imageChecker.Exists(context.Background(), deployJobImage(p.config, input))
	if err != nil {
		return false
	}
	return !exists
}

// deployJobImage is the tenant's own <tenant>-devops runtime image at the
// requested version, pulled from the configured registry.
func deployJobImage(config EnvDeployConfig, input EnvProvisionInput) string {
	return fmt.Sprintf("%s/%s-devops:%s", config.Registry, input.Tenant, input.Version)
}

// canonicalRuntimeImage is the published erun-devops image every hosted deploy
// falls back to when the tenant has never published its own <tenant>-devops
// image — the same canonical image `erun deploy --runtime-image` installs on an
// operator's own machine.
func canonicalRuntimeImage(config EnvDeployConfig, input EnvProvisionInput) string {
	return fmt.Sprintf("%s/%s:%s", config.Registry, eruncommon.DevopsComponentName, input.Version)
}

// deployJobParams renders the placement for one env-deploy Job. A tenant that
// has published its own image deploys with it, carrying its baked deploy
// config (the image-baked config model), pulled from the configured registry
// at the requested version. A tenant with no image (input.Bootstrap, decided
// once by resolveBootstrapImage before the durable workflow runs) instead runs
// the Job on the canonical published erun-devops image and threads
// --runtime-image so the installed runtime chart matches it, rather than
// resolving artifacts that were never published.
func deployJobParams(config EnvDeployConfig, input EnvProvisionInput) deployexec.DeployJobParams {
	image := deployJobImage(config, input)
	runtimeImageOverride := ""
	if input.Bootstrap {
		image = canonicalRuntimeImage(config, input)
		runtimeImageOverride = image
	}
	return deployexec.DeployJobParams{
		Tenant:               input.Tenant,
		Environment:          input.Environment,
		Version:              input.Version,
		DeployID:             input.DeployID,
		Namespace:            config.PlatformNamespace,
		Image:                image,
		RuntimeImageOverride: runtimeImageOverride,
		ServiceAccount:       config.DeployerServiceAccount,
		ExposeTargetIP:       config.ExposeTargetIP,
		MaxCPU:               namespaceQuotaCPUQuantity(input.MaxCPUMillicores),
		MaxMemory:            namespaceQuotaMemoryQuantity(input.MaxMemoryMB),
		MaxStorage:           namespaceQuotaStorageQuantity(input.MaxStorageGB),
		MaxCPUMillicores:     input.MaxCPUMillicores,
		MaxMemoryMB:          input.MaxMemoryMB,
		MaxStorageGB:         input.MaxStorageGB,
	}
}

// namespaceQuotaCPUQuantity/MemoryQuantity/StorageQuantity render the tenant's
// millicore/MiB/GiB caps as Kubernetes quantity strings. Zero (should not
// happen; see EnvProvisionInput's doc) renders empty, which
// deployexec.namespaceQuotaFlags treats as "no cap configured".
func namespaceQuotaCPUQuantity(millicores int) string {
	if millicores <= 0 {
		return ""
	}
	return fmt.Sprintf("%dm", millicores)
}

func namespaceQuotaMemoryQuantity(mb int) string {
	if mb <= 0 {
		return ""
	}
	return fmt.Sprintf("%dMi", mb)
}

func namespaceQuotaStorageQuantity(gb int) string {
	if gb <= 0 {
		return ""
	}
	return fmt.Sprintf("%dGi", gb)
}

func (p *EnvProvisioner) provisionWorkflow(dctx dbos.DBOSContext, input EnvProvisionInput) (string, error) {
	params := deployJobParams(p.config, input)
	// One step: the coordinator is idempotent on re-run (it re-marks provisioning
	// and the Job create tolerates an already-exists, then re-watches), so a
	// mid-deploy restart resumes cleanly without a second Job.
	return dbos.RunAsStep(dctx, func(c context.Context) (string, error) {
		scoped := security.WithContext(c, security.Context{
			TenantID:   input.TenantID,
			TenantType: input.TenantType,
			ErunUserID: input.ErunUserID,
		})
		if err := p.coordinator.Provision(scoped, input.EnvironmentID, params); err != nil {
			return "failed", err
		}
		return "running", nil
	})
}
