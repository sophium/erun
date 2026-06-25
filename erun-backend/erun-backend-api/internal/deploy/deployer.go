// Package deploy runs the durable runtime deploy of an environment into its
// provisioned cloud context: it reads the env's running context + the custodied
// k3s admin token, builds an in-memory Kubernetes REST config that addresses the
// cluster's token-authed :6443 API server, and installs the published runtime
// chart at the env's version into the per-env namespace — all in-process via the
// Kubernetes and Helm Go SDKs (no kubectl/helm/aws subprocess), as a DBOS durable
// workflow so a control-plane restart resumes from the last completed step
// (issue #680/#681, part of #605/#660).
//
// This is the orchestrator half of the primitive/orchestration split (root
// AGENTS.md § "Command primitives vs orchestration"): it threads an explicit,
// already-pushed version and installs it. It never builds or pushes; a missing
// version is a hard error here, not a trigger to build.
package deploy

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/google/uuid"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	eruncommon "github.com/sophium/erun/erun-common"
)

const (
	statusRunning   = "running"
	statusDeploying = "deploying"
	statusDeployed  = "deployed"
	statusFailed    = "failed"
	// markStatusMaxRetries bounds the retries on the lifecycle status-write steps
	// so a transient DB blip does not leave the env stranded at "deploying".
	markStatusMaxRetries = 5
)

// DeployInput is the serializable workflow input DBOS checkpoints. It carries
// only the tenant identity (to rebuild the RLS security context inside each
// step), the env the deploy targets, and the already-pushed runtime version to
// install — never a secret. Everything else (the env's name + context, the
// tenant's name, the cluster's public IP, the k3s token) is read fresh from the
// database inside the step, so a public-IP change between request and execution
// resolves correctly and nothing mutable is frozen into the checkpoint.
type DeployInput struct {
	TenantID      string `json:"tenantId"`
	TenantType    string `json:"tenantType"`
	ErunUserID    string `json:"erunUserId,omitempty"`
	EnvironmentID string `json:"environmentId"`
	// Version is the already-pushed runtime version the chart + image are pinned
	// to (minted by build, threaded by the caller; never synthesized here).
	Version string `json:"version"`
}

// DeployResult is the workflow output (non-secret).
type DeployResult struct {
	Namespace string `json:"namespace"`
	Release   string `json:"release"`
	Version   string `json:"version"`
	Status    string `json:"status"`
}

// EnvDeployer owns the durable runtime-deploy workflow and its dependencies.
type EnvDeployer struct {
	dbosCtx       dbos.DBOSContext
	environments  *repository.EnvironmentRepository
	contexts      *repository.ContextRepository
	credentials   *repository.ContextCredentialRepository
	tenants       *repository.TenantRepository
	tenantIssuers *repository.TenantIssuerRepository
	// runtimeRegistry is where the published runtime chart + image live; the
	// executor addresses oci://<runtimeRegistry>/charts/erun-devops.
	runtimeRegistry string
	// chartPathOverride / imageOverride are verification seams (mirroring the
	// provisioner's awsEndpoint): zero values give the production path (published
	// OCI chart, chart-default images).
	chartPathOverride string
	imageOverride     string
	// wait makes helm block until the rollout is healthy before marking the env
	// deployed (production). The verification path disables it: the stand-in pod
	// never reaches readiness (the dind probe), so waiting would always time out.
	wait bool
	// registryPlainHTTP makes the helm OCI registry client use plain HTTP — a
	// verification seam for a local registry (registry:2). Production pulls the
	// published chart from ghcr over HTTPS, so this stays false there.
	registryPlainHTTP bool
	workflowFn        func(dbos.DBOSContext, DeployInput) (DeployResult, error)
}

// EnvDeployOptions configures an EnvDeployer. The zero value is production: the
// published OCI runtime chart at ghcr.io/sophium, chart-default images, and a
// helm install that waits for the rollout to be healthy.
type EnvDeployOptions struct {
	// RuntimeRegistry is where the published runtime chart + image live. Empty
	// defaults to ghcr.io/sophium (eruncommon.DefaultContainerRegistry).
	RuntimeRegistry string
	// ChartPathOverride, when set, makes the executor install this local chart
	// directory instead of the published OCI chart — a verification seam (Lima
	// k3s) so the durable deploy workflow can run without the published chart or
	// the ~1GB runtime image. Empty = production (published OCI chart).
	ChartPathOverride string
	// ImageOverride, when set, pins imageOverrides.erun-devops to this image (a
	// tiny stand-in for verification). Empty = chart default.
	ImageOverride string
	// NoWait disables helm's wait-for-rollout (a verification seam): the local
	// chart deploys a stand-in image whose pod never reaches readiness, so
	// waiting would always time out. Zero value = production (waits).
	NoWait bool
	// RegistryPlainHTTP makes the helm OCI registry client use plain HTTP — a
	// verification seam for a local registry. Zero value = production (HTTPS).
	RegistryPlainHTTP bool
}

// NewEnvDeployer builds the deployer and registers its workflow with DBOS. Call
// before dbos.Launch.
func NewEnvDeployer(
	dbosCtx dbos.DBOSContext,
	environments *repository.EnvironmentRepository,
	contexts *repository.ContextRepository,
	credentials *repository.ContextCredentialRepository,
	tenants *repository.TenantRepository,
	tenantIssuers *repository.TenantIssuerRepository,
	opts EnvDeployOptions,
) *EnvDeployer {
	registry := strings.TrimSpace(opts.RuntimeRegistry)
	if registry == "" {
		registry = eruncommon.DefaultContainerRegistry
	}
	d := &EnvDeployer{
		dbosCtx:           dbosCtx,
		environments:      environments,
		contexts:          contexts,
		credentials:       credentials,
		tenants:           tenants,
		tenantIssuers:     tenantIssuers,
		runtimeRegistry:   registry,
		chartPathOverride: strings.TrimSpace(opts.ChartPathOverride),
		imageOverride:     strings.TrimSpace(opts.ImageOverride),
		wait:              !opts.NoWait,
		registryPlainHTTP: opts.RegistryPlainHTTP,
	}
	// A method value: a stable workflow name across restarts (so DBOS recovers
	// it) that also captures d's dependencies. Registered once here and reused by
	// Start, so RegisterWorkflow and RunWorkflow see the same function.
	d.workflowFn = d.deployWorkflow
	dbos.RegisterWorkflow(dbosCtx, d.workflowFn)
	return d
}

// Start kicks off the runtime deploy asynchronously. The HTTP handler returns
// immediately; the durable workflow drives the (minutes-long) helm rollout and
// updates the env's deploy status. Each call gets a fresh workflow ID, so a
// re-deploy — including a retry after a failure, or recovering an env left
// "deploying" by a control-plane crash — always starts a new run rather than
// returning a terminal workflow's cached result. (A DBOS workflow ID is
// terminal once it succeeds/fails, so a stable per-(env,version) ID could never
// retry.) DBOS still recovers an in-flight run by its persisted ID after a
// restart; the env-id/version prefix keeps the workflow list legible.
func (d *EnvDeployer) Start(input DeployInput) error {
	id := "envdeploy-" + input.EnvironmentID + "-" + input.Version + "-" + uuid.NewString()
	_, err := dbos.RunWorkflow(d.dbosCtx, d.workflowFn, input, dbos.WithWorkflowID(id))
	return err
}

func (d *EnvDeployer) deployWorkflow(dctx dbos.DBOSContext, input DeployInput) (DeployResult, error) {
	result, err := dbos.RunAsStep(dctx, func(c context.Context) (DeployResult, error) {
		return d.deployEnv(c, input)
	})
	if err != nil {
		// Record the failure in its own checkpointed step (with bounded retries so
		// a transient DB blip does not strand the env at "deploying"). If even the
		// retries fail, log it so the stranded row is diagnosable rather than silent
		// — a stale "deploying" is re-claimable by a later re-deploy.
		if _, markErr := dbos.RunAsStep(dctx, func(c context.Context) (string, error) {
			return "", d.markFailed(c, input, err)
		}, dbos.WithStepMaxRetries(markStatusMaxRetries)); markErr != nil {
			log.Printf("erun deploy: could not record failed status for env %s: %v (deploy error: %v)", input.EnvironmentID, markErr, err)
		}
		return DeployResult{}, err
	}
	if _, markErr := dbos.RunAsStep(dctx, func(c context.Context) (string, error) {
		return "", d.markDeployed(c, input)
	}, dbos.WithStepMaxRetries(markStatusMaxRetries)); markErr != nil {
		// The chart deployed but recording it failed; the env stays "deploying"
		// until a re-deploy reconciles it (helm upgrade is idempotent). Surface it.
		log.Printf("erun deploy: env %s deployed but recording the status failed: %v", input.EnvironmentID, markErr)
		return DeployResult{}, markErr
	}
	return result, nil
}

// deployEnv resolves the env's running context, reads the custodied k3s token,
// builds an in-memory REST config that addresses the cluster, ensures the per-env
// namespace, and installs the runtime chart at the env's version — all via the
// Kubernetes + Helm Go SDKs, no subprocess. The token never leaves this step
// (it is not part of any DBOS step-output checkpoint).
func (d *EnvDeployer) deployEnv(c context.Context, input DeployInput) (DeployResult, error) {
	sc := d.scoped(c, input)
	env, err := d.environments.Get(sc, input.EnvironmentID)
	if err != nil {
		return DeployResult{}, fmt.Errorf("resolve environment %q: %w", input.EnvironmentID, err)
	}
	if strings.TrimSpace(env.ContextID) == "" {
		return DeployResult{}, fmt.Errorf("environment %q has no context to deploy into", input.EnvironmentID)
	}
	tenant, err := d.tenants.Current(sc)
	if err != nil {
		return DeployResult{}, fmt.Errorf("resolve tenant: %w", err)
	}
	ctxRow, err := d.contexts.Get(sc, env.ContextID)
	if err != nil {
		return DeployResult{}, fmt.Errorf("resolve context %q: %w", env.ContextID, err)
	}
	if ctxRow.Status != statusRunning {
		return DeployResult{}, fmt.Errorf("context %q is not provisioned (status %q)", env.ContextID, ctxRow.Status)
	}
	if strings.TrimSpace(ctxRow.PublicIP) == "" {
		return DeployResult{}, fmt.Errorf("context %q has no public ip", env.ContextID)
	}
	// The custodied token is the source of truth for what the instance baked in
	// (the provisioner derives it deterministically and stores it; reading custody
	// stays correct even if a future provision run mints a random token instead).
	token, err := d.credentials.Get(sc, env.ContextID)
	if err != nil {
		return DeployResult{}, fmt.Errorf("read custodied k3s admin token: %w", err)
	}

	namespace := eruncommon.KubernetesNamespaceName(tenant.Name, env.Name)
	release := eruncommon.RuntimeReleaseName(tenant.Name)

	cfg := restConfig(ctxRow.PublicIP, token)
	if err := ensureNamespace(c, cfg, namespace); err != nil {
		return DeployResult{}, fmt.Errorf("ensure namespace %q: %w", namespace, err)
	}

	chartRef := d.chartPathOverride
	if chartRef == "" {
		chartRef = eruncommon.PublishedDevopsChartOCIRepo(d.runtimeRegistry) + "/" + eruncommon.DevopsComponentName
	}
	// Authenticate the env's erun-mcp edge against the tenant's OIDC issuer (#685)
	// so the console can later drive it with a token that issuer mints. No issuer
	// (e.g. a file://-only tenant) leaves the edge loopback-only — back-compat.
	mcpIssuer, err := d.resolveMCPIssuer(sc)
	if err != nil {
		return DeployResult{}, fmt.Errorf("resolve tenant mcp issuer: %w", err)
	}
	mcpAudience := ""
	if mcpIssuer != "" {
		mcpAudience = eruncommon.MCPTokenAudience(tenant.Name, env.Name)
	}

	values := runtimeValues(tenant.Name, env.Name, ctxRow, d.runtimeRegistry, d.imageOverride, mcpIssuer, mcpAudience)
	if err := helmDeploy(c, cfg, release, namespace, chartRef, input.Version, values, d.wait, d.registryPlainHTTP); err != nil {
		return DeployResult{}, fmt.Errorf("deploy runtime chart: %w", err)
	}
	return DeployResult{Namespace: namespace, Release: release, Version: input.Version, Status: statusDeployed}, nil
}

// resolveMCPIssuer returns the tenant's first OIDC issuer — any registered
// issuer that is not a `file://` desktop-key issuer (the MCP edge dispatches
// every non-file:// issuer to its OIDC/JWKS path, so http:// and https:// both
// qualify). The env's erun-mcp edge is configured to trust it (#685). A tenant
// with only a file:// issuer (or none) yields "", leaving the edge loopback-only.
// A nil repository (older wiring) also yields "". A genuine lookup error is
// surfaced so a deploy never silently ships a mis-trusted edge.
func (d *EnvDeployer) resolveMCPIssuer(ctx context.Context) (string, error) {
	if d.tenantIssuers == nil {
		return "", nil
	}
	issuers, err := d.tenantIssuers.List(ctx)
	if err != nil {
		return "", err
	}
	return selectMCPIssuer(issuers), nil
}

// selectMCPIssuer returns the first registered issuer that is not a `file://`
// desktop-key issuer — the OIDC issuer the env's MCP edge will trust. Pure (no
// DB) so the selection rule is unit-tested directly.
func selectMCPIssuer(issuers []model.TenantIssuer) string {
	for _, ti := range issuers {
		if issuer := strings.TrimSpace(ti.Issuer); issuer != "" && !strings.HasPrefix(issuer, "file://") {
			return issuer
		}
	}
	return ""
}

func (d *EnvDeployer) markDeployed(c context.Context, input DeployInput) error {
	return d.environments.UpdateDeployResult(d.scoped(c, input), input.EnvironmentID, statusDeployed, input.Version, "")
}

func (d *EnvDeployer) markFailed(c context.Context, input DeployInput, cause error) error {
	return d.environments.UpdateDeployResult(d.scoped(c, input), input.EnvironmentID, statusFailed, "", cause.Error())
}

// scoped rebuilds the request-scoped security context inside a workflow step so
// the repositories' RLS transaction wiring binds to the right tenant.
func (d *EnvDeployer) scoped(c context.Context, input DeployInput) context.Context {
	return security.WithContext(c, security.Context{
		TenantID:   input.TenantID,
		TenantType: input.TenantType,
		ErunUserID: input.ErunUserID,
	})
}
