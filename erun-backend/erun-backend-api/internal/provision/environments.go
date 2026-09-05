package provision

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/deployexec"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// DNS01TokenSigner mints the per-env DNS-01 broker token deployJobParams
// threads to the deploy Job so it can provision the env's own per-env TLS
// Issuer+Certificate through the DNS-01 broker. Satisfied by
// *mcptoken.Signer without this package importing it.
type DNS01TokenSigner interface {
	SignDNS01(tenant, environment string, now time.Time) (token, audience string, err error)
}

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
	// ContextID/PlacementKubernetesContext/PlacementServerURL name the target
	// cluster (#1112) this environment was placed on at create time: empty
	// means the platform's own cluster, exactly as every environment placed
	// before this existed. Non-secret coordinates only — the admin-token
	// credential is resolved fresh from ContextCredentialRepository at
	// Job-build time (EnvironmentProvisioner.Provision), never checkpointed
	// here.
	ContextID                  string `json:"contextId,omitempty"`
	PlacementKubernetesContext string `json:"placementKubernetesContext,omitempty"`
	PlacementServerURL         string `json:"placementServerUrl,omitempty"`
	// DeployID identifies one explicit deploy attempt. Being part of the
	// checkpointed input is what lets a resumed workflow rebuild the same Job
	// name and re-watch its own run. Empty on the create path, which deploys an
	// environment exactly once.
	DeployID string `json:"deployId,omitempty"`
	// MaxCPUMillicores/MaxMemoryMB/MaxStorageGB are the caller's tenant_quotas
	// row at request time, threaded into the deploy Job as --max-cpu/
	// --max-memory/--max-storage so the namespace gets a real ResourceQuota +
	// LimitRange. Zero on all three (should not happen: routes always
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
	// exactly as they were before automatic exposure existed: this field, unlike
	// the three above, does not gate the executor itself (newEnvironmentProvisioner
	// stays enabled without it) — it only decides whether a deploy also chains
	// an expose.
	ExposeTargetIP string
	// ExposeServicesZone/ExposePlatformNamespace thread deployexec.DeployJobParams'
	// fields of the same name — the platform coordinates the chained `erun
	// expose` needs but the sourceless deploy Job cannot resolve on its own.
	// See deployexec.DeployJobParams for why the control plane already
	// carries both.
	ExposeServicesZone      string
	ExposePlatformNamespace string
	// ImagePullSecrets names the dockerconfigjson Secrets in PlatformNamespace
	// that hold the registry credentials the deploy Job pulls with. The
	// published-image precondition probes the registry with the same credential,
	// which is the only way it can tell a tenant image that was never published
	// from one in a private namespace it may not look into. Empty leaves the
	// probe unauthenticated, so it stays inconclusive and no deploy is diverted.
	ImagePullSecrets []string
	// MCPAuthPublicKeyPEM is the backend's own MCP-signing public key,
	// threaded to every deploy Job as DeployJobParams.MCPAuthPublicKeyPEM so
	// the runtime's MCP edge trusts tokens the backend mints for the console
	// Empty (no MCP signing key configured) leaves every deploy Job
	// exactly as it was before this existed — the edge stays loopback-only.
	MCPAuthPublicKeyPEM string
	// TLSCertSigner/TLSBrokerURL/TLSWebhookGroup/ACMEEmail/ACMEServer provision
	// the env's per-env wildcard TLS certificate through the DNS-01 broker:
	// deployJobParams mints a per-env token with TLSCertSigner and
	// threads it plus these coordinates to the deploy Job, which passes them to
	// the chained `erun expose` (see deployexec.DeployJobParams). A nil signer
	// or an empty TLSBrokerURL/ACMEEmail leaves every deploy Job exactly as it
	// was before this existed — expose still applies the Ingress/DNS, its TLS
	// secretName just never gets populated.
	TLSCertSigner   DNS01TokenSigner
	TLSBrokerURL    string
	TLSWebhookGroup string
	ACMEEmail       string
	ACMEServer      string
	// PlatformTenant is this instance's own declared tenant identity
	// (ERUN_TENANT), threaded here so resolveBootstrapImage can name a
	// runtime-image fallback substitution: a platform's own
	// OPERATIONS tenant bootstrapped under a legacy name keeps resolving that
	// legacy <name>-devops image, confirms it missing, and silently falls
	// back to the canonical erun-devops image even when the platform actually
	// publishes <declared>-devops. Empty (ERUN_TENANT unset) never reports a
	// substitution, matching every deploy before this existed.
	PlatformTenant string
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
// project has never run `erun push`, so its own image can never exist. The
// canonical image is handed to the checker as the control reference, since it
// is the one image this deploy already requires to exist. A nil checker (not
// wired) or an inconclusive probe leaves Bootstrap false, matching
// RuntimeImageChecker's fail-open contract: only a registry-confirmed absence
// selects the fallback, never a network hiccup or an unreadable namespace.
func (p *EnvProvisioner) resolveBootstrapImage(input EnvProvisionInput) bool {
	_, bootstrap := ResolveRuntimeImage(context.Background(), p.imageChecker, p.config.Registry, input.Tenant, input.Version)
	if bootstrap {
		p.reportRuntimeImageFallbackSubstitution(input)
	}
	return bootstrap
}

// reportRuntimeImageFallbackSubstitution names the runtime-image fallback
// when it may be masking a real published image: a platform's own
// OPERATIONS tenant bootstrapped under a legacy name (see
// repository.defaultBootstrapTenantName) keeps resolving that legacy
// <name>-devops image, confirms it missing, and falls back to the canonical
// erun-devops image — correct when the tenant genuinely has no image, but
// misleading when the platform actually publishes <declared>-devops under its
// ERUN_TENANT name and this environment's own tenant is simply named
// differently. Only checked for the platform's own OPERATIONS tenant: an
// ordinary tenant with no image has no other name to have published under.
// Reports nothing unless the differently-named image is affirmatively
// confirmed present, matching RuntimeImageChecker's fail-open contract.
func (p *EnvProvisioner) reportRuntimeImageFallbackSubstitution(input EnvProvisionInput) {
	platformTenant := strings.TrimSpace(p.config.PlatformTenant)
	if platformTenant == "" || platformTenant == input.Tenant || input.TenantType != string(model.TenantTypeOperations) || p.imageChecker == nil {
		return
	}
	canonical := CanonicalRuntimeImage(p.config.Registry, input.Version)
	declared := TenantRuntimeImage(p.config.Registry, platformTenant, input.Version)
	present, err := p.imageChecker.ConfirmedPresent(context.Background(), declared, canonical)
	if err != nil || !present {
		return
	}
	log.Printf("erun api: runtime image fallback substitution: operations tenant %q has no own %s image and fell back to canonical %s, but ERUN_TENANT=%q has a published %s -- this tenant's name likely disagrees with the platform's declared identity; reconcile via PATCH /v1/tenants/reconcile-bootstrap-name",
		input.Tenant, TenantRuntimeImage(p.config.Registry, input.Tenant, input.Version), canonical, platformTenant, declared)
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
	image := TenantRuntimeImage(config.Registry, input.Tenant, input.Version)
	runtimeImageOverride := ""
	if input.Bootstrap {
		image = CanonicalRuntimeImage(config.Registry, input.Version)
		runtimeImageOverride = image
	}
	params := deployexec.DeployJobParams{
		Tenant:      input.Tenant,
		Environment: input.Environment,
		Version:     input.Version,
		DeployID:    input.DeployID,
		Namespace:   config.PlatformNamespace,
		Image:       image,
		// AdminToken is deliberately absent here: deployJobParams is a pure
		// function with no DB access, called before the durable step runs.
		// EnvironmentProvisioner.Provision resolves the live credential and
		// sets it on this Placement immediately before the Job runs.
		Placement: deployexec.PlacementParams{
			ContextID:         input.ContextID,
			KubernetesContext: input.PlacementKubernetesContext,
			ServerURL:         input.PlacementServerURL,
		},
		RuntimeImageOverride:    runtimeImageOverride,
		ServiceAccount:          config.DeployerServiceAccount,
		ExposeTargetIP:          config.ExposeTargetIP,
		ExposeServicesZone:      config.ExposeServicesZone,
		ExposePlatformNamespace: config.ExposePlatformNamespace,
		MaxCPU:                  namespaceQuotaCPUQuantity(input.MaxCPUMillicores),
		MaxMemory:               namespaceQuotaMemoryQuantity(input.MaxMemoryMB),
		MaxStorage:              namespaceQuotaStorageQuantity(input.MaxStorageGB),
		MaxCPUMillicores:        input.MaxCPUMillicores,
		MaxMemoryMB:             input.MaxMemoryMB,
		MaxStorageGB:            input.MaxStorageGB,
		MCPAuthPublicKeyPEM:     config.MCPAuthPublicKeyPEM,
	}
	applyTLSCertParams(&params, config, input)
	return params
}

// applyTLSCertParams mints the per-env DNS-01 broker token and threads the
// broker/ACME coordinates onto the deploy Job params when TLS provisioning is
// configured. A signing failure is logged and skipped rather than
// failing the deploy — the same best-effort posture exposure already has: a
// missing per-env certificate leaves the env reachable over self-signed TLS
// rather than not running at all.
func applyTLSCertParams(params *deployexec.DeployJobParams, config EnvDeployConfig, input EnvProvisionInput) {
	if config.TLSCertSigner == nil || strings.TrimSpace(config.TLSBrokerURL) == "" || strings.TrimSpace(config.ACMEEmail) == "" {
		return
	}
	token, _, err := config.TLSCertSigner.SignDNS01(input.Tenant, input.Environment, time.Now())
	if err != nil {
		log.Printf("erun api: mint dns01 token for %s/%s: %v", input.Tenant, input.Environment, err)
		return
	}
	params.TLSDNS01Token = token
	params.TLSDNS01BrokerURL = config.TLSBrokerURL
	params.TLSDNS01WebhookGroupName = config.TLSWebhookGroup
	params.TLSACMEEmail = config.ACMEEmail
	params.TLSACMEServer = config.ACMEServer
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
