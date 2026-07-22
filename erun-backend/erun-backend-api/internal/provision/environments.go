package provision

import (
	"context"
	"fmt"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

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
}

// EnvProvisioner runs the durable env-deploy workflow, so a control-plane restart
// resumes it rather than re-running a completed deploy. It wraps the coordinator
// in a DBOS workflow keyed by environment id, so a retried create never
// double-deploys.
type EnvProvisioner struct {
	dbosCtx     dbos.DBOSContext
	coordinator EnvCoordinator
	config      EnvDeployConfig
	workflowFn  func(dbos.DBOSContext, EnvProvisionInput) (string, error)
}

func NewEnvProvisioner(dbosCtx dbos.DBOSContext, coordinator EnvCoordinator, config EnvDeployConfig) *EnvProvisioner {
	p := &EnvProvisioner{dbosCtx: dbosCtx, coordinator: coordinator, config: config}
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
	_, err := dbos.RunWorkflow(p.dbosCtx, p.workflowFn, input, dbos.WithWorkflowID("provision-env-"+input.EnvironmentID))
	return err
}

// deployJobParams renders the placement for one env-deploy Job. The env deploys
// with the tenant's own <tenant>-devops runtime image, which carries its baked
// deploy config (the image-baked config model), pulled from the configured
// registry at the requested version.
func deployJobParams(config EnvDeployConfig, input EnvProvisionInput) deployexec.DeployJobParams {
	return deployexec.DeployJobParams{
		Tenant:         input.Tenant,
		Environment:    input.Environment,
		Version:        input.Version,
		Namespace:      config.PlatformNamespace,
		Image:          fmt.Sprintf("%s/%s-devops:%s", config.Registry, input.Tenant, input.Version),
		ServiceAccount: config.DeployerServiceAccount,
	}
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
