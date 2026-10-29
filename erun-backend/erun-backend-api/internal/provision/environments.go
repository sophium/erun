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

// DNS01TokenSigner mints per-env DNS-01 broker tokens for deploy Jobs.
type DNS01TokenSigner interface {
	SignDNS01(tenant, environment string, now time.Time) (token, audience string, err error)
}

// EnvProvisionInput is the checkpointed workflow input; it carries no secrets.
type EnvProvisionInput struct {
	TenantID      string `json:"tenantId"`
	TenantType    string `json:"tenantType"`
	ErunUserID    string `json:"erunUserId,omitempty"`
	EnvironmentID string `json:"environmentId"`
	Tenant        string `json:"tenant"`
	Environment   string `json:"environment"`
	Version       string `json:"version"`
	// Placement coordinates are non-secret; empty means the platform's own
	// cluster. The admin token is resolved at Job-build time, never checkpointed.
	ContextID                  string `json:"contextId,omitempty"`
	PlacementKubernetesContext string `json:"placementKubernetesContext,omitempty"`
	PlacementServerURL         string `json:"placementServerUrl,omitempty"`
	// DeployID lets a resumed workflow re-watch its own Job; empty on create.
	DeployID string `json:"deployId,omitempty"`
	// Quota caps become --max-cpu/--max-memory/--max-storage; all zero skips the flags.
	MaxCPUMillicores int `json:"maxCpuMillicores,omitempty"`
	MaxMemoryMB      int `json:"maxMemoryMb,omitempty"`
	MaxStorageGB     int `json:"maxStorageGb,omitempty"`
	// Bootstrap is checkpointed so a resume does not re-probe the registry; true
	// means the tenant's own image is confirmed missing.
	Bootstrap bool `json:"bootstrap,omitempty"`
}

// EnvCoordinator runs an env deploy to a terminal status.
type EnvCoordinator interface {
	Provision(ctx context.Context, environmentID string, params deployexec.DeployJobParams) error
}

// EnvDeployConfig is the per-instance placement a deploy Job needs.
type EnvDeployConfig struct {
	Registry               string
	PlatformNamespace      string
	DeployerServiceAccount string
	// ExposeTargetIP is the ingress IP; empty disables the chained expose but not deploys.
	ExposeTargetIP string
	// The sourceless deploy Job cannot resolve these for `erun expose` itself.
	ExposeServicesZone      string
	ExposePlatformNamespace string
	// ImagePullSecrets also authenticate the published-image probe; empty leaves it
	// inconclusive, so no deploy is diverted.
	ImagePullSecrets []string
	// MCPAuthPublicKeyPEM lets the MCP edge trust backend-minted tokens; empty keeps it loopback-only.
	MCPAuthPublicKeyPEM string
	// TLS fields provision the per-env wildcard certificate via the DNS-01 broker.
	// A nil signer or empty TLSBrokerURL/ACMEEmail leaves TLS unpopulated.
	TLSCertSigner   DNS01TokenSigner
	TLSBrokerURL    string
	TLSWebhookGroup string
	ACMEEmail       string
	ACMEServer      string
	// PlatformTenant (ERUN_TENANT) lets resolveBootstrapImage report when a legacy
	// tenant name masks a published <declared>-devops image. Empty reports nothing.
	PlatformTenant string
}

// EnvProvisioner runs the durable deploy workflow so a restart resumes it.
type EnvProvisioner struct {
	dbosCtx      dbos.DBOSContext
	coordinator  EnvCoordinator
	config       EnvDeployConfig
	imageChecker RuntimeImageChecker
	workflowFn   func(dbos.DBOSContext, EnvProvisionInput) (string, error)
}

// NewEnvProvisioner accepts a nil imageChecker, which skips the published-image precondition.
func NewEnvProvisioner(dbosCtx dbos.DBOSContext, coordinator EnvCoordinator, config EnvDeployConfig, imageChecker RuntimeImageChecker) *EnvProvisioner {
	p := &EnvProvisioner{dbosCtx: dbosCtx, coordinator: coordinator, config: config, imageChecker: imageChecker}
	// DBOS names and recovers the workflow by this one stable function value.
	p.workflowFn = p.provisionWorkflow
	dbos.RegisterWorkflow(dbosCtx, p.workflowFn)
	return p
}

// Start runs provisioning asynchronously, keyed by environment id so a retried
// create does not deploy twice.
func (p *EnvProvisioner) Start(input EnvProvisionInput) error {
	input.Bootstrap = p.resolveBootstrapImage(input)
	_, err := dbos.RunWorkflow(p.dbosCtx, p.workflowFn, input, dbos.WithWorkflowID("provision-env-"+input.EnvironmentID))
	return err
}

// StartDeploy is keyed by attempt: an environment-keyed id would make any
// re-deploy a no-op. The deploy claim guards concurrency.
func (p *EnvProvisioner) StartDeploy(input EnvProvisionInput) error {
	input.Bootstrap = p.resolveBootstrapImage(input)
	_, err := dbos.RunWorkflow(p.dbosCtx, p.workflowFn, input, dbos.WithWorkflowID("deploy-env-"+input.EnvironmentID+"-"+input.DeployID))
	return err
}

// resolveBootstrapImage reports whether the tenant's own image is confirmed
// missing. A nil checker or inconclusive probe fails open to false.
func (p *EnvProvisioner) resolveBootstrapImage(input EnvProvisionInput) bool {
	_, bootstrap := ResolveRuntimeImage(context.Background(), p.imageChecker, p.config.Registry, input.Tenant, input.Version)
	if bootstrap {
		p.reportRuntimeImageFallbackSubstitution(input)
	}
	return bootstrap
}

// reportRuntimeImageFallbackSubstitution warns when the platform's OPERATIONS
// tenant, under a legacy name, falls back although <declared>-devops is published.
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

// deployJobParams renders one deploy Job's placement, using the canonical
// erun-devops image with --runtime-image when input.Bootstrap is set.
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
		// AdminToken is set by EnvironmentProvisioner.Provision just before the Job runs.
		Placement: deployexec.PlacementParams{
			TenantID:          input.TenantID,
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

// applyTLSCertParams is best-effort: a signing failure leaves self-signed TLS
// rather than failing the deploy.
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

// Zero renders empty, which deployexec.namespaceQuotaFlags treats as no cap.
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
	// The coordinator is idempotent on re-run, so a restart resumes without a second Job.
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
