// Package deploy runs the durable runtime deploy of an environment into its
// provisioned cloud context: it reads the env's running context + the custodied
// k3s admin token, materializes a kube-context that addresses the cluster's
// token-authed :6443 API server, and helm-installs the published runtime chart
// at the env's version into the per-env namespace — all as a DBOS durable
// workflow so a control-plane restart resumes from the last completed step
// (issue #680, part of #605/#660).
//
// This is the orchestrator half of the primitive/orchestration split (root
// AGENTS.md § "Command primitives vs orchestration"): it composes the pure
// `deploy` primitive (eruncommon.RunHelmDeploy) and threads an explicit,
// already-pushed version. It never builds or pushes; a missing version is a
// hard error here, not a trigger to build.
package deploy

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

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
	dbosCtx      dbos.DBOSContext
	environments *repository.EnvironmentRepository
	contexts     *repository.ContextRepository
	credentials  *repository.ContextCredentialRepository
	tenants      *repository.TenantRepository
	// runtimeRegistry is where the published runtime chart + image live; the
	// executor addresses oci://<runtimeRegistry>/charts/erun-devops.
	runtimeRegistry string
	// chartPathOverride / imageOverride / deployer are verification seams
	// (mirroring the provisioner's awsEndpoint): zero values give the production
	// path (published OCI chart, real --wait DeployHelmChart).
	chartPathOverride string
	imageOverride     string
	deployer          eruncommon.HelmChartDeployerFunc
	// kubeMu serializes the kubeconfig writes (set-cluster/-credentials/-context)
	// so concurrent deploys do not race on the shared kubeconfig file the helm /
	// kubectl subprocesses read; the long helm rollout itself runs concurrently.
	kubeMu     sync.Mutex
	workflowFn func(dbos.DBOSContext, DeployInput) (DeployResult, error)
}

// EnvDeployOptions configures an EnvDeployer. The zero value is production: the
// published OCI runtime chart at ghcr.io/sophium and the real namespace-ensure
// wrapped DeployHelmChart (helm upgrade --install --wait).
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
	// Deployer overrides the helm deployer (default: namespace-ensure-wrapped
	// eruncommon.DeployHelmChart). The Lima verification injects a no-wait
	// deployer so it can assert the release + objects land without blocking on
	// the runtime pod's dind readiness probe (the chart hardcodes --wait).
	Deployer eruncommon.HelmChartDeployerFunc
}

// NewEnvDeployer builds the deployer and registers its workflow with DBOS. Call
// before dbos.Launch.
func NewEnvDeployer(
	dbosCtx dbos.DBOSContext,
	environments *repository.EnvironmentRepository,
	contexts *repository.ContextRepository,
	credentials *repository.ContextCredentialRepository,
	tenants *repository.TenantRepository,
	opts EnvDeployOptions,
) *EnvDeployer {
	registry := strings.TrimSpace(opts.RuntimeRegistry)
	if registry == "" {
		registry = eruncommon.DefaultContainerRegistry
	}
	deployer := opts.Deployer
	if deployer == nil {
		deployer = eruncommon.WrapHelmChartDeployerWithNamespaceEnsure(eruncommon.EnsureKubernetesNamespace, eruncommon.DeployHelmChart)
	}
	d := &EnvDeployer{
		dbosCtx:           dbosCtx,
		environments:      environments,
		contexts:          contexts,
		credentials:       credentials,
		tenants:           tenants,
		runtimeRegistry:   registry,
		chartPathOverride: strings.TrimSpace(opts.ChartPathOverride),
		imageOverride:     strings.TrimSpace(opts.ImageOverride),
		deployer:          deployer,
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
// updates the env's deploy status. The (environment, version) pair is the
// idempotency key, so a double-submit of the same version does not start a
// second rollout and a control-plane restart resumes the in-flight one. A
// re-deploy at a new version is a new workflow.
func (d *EnvDeployer) Start(input DeployInput) error {
	id := "envdeploy-" + input.EnvironmentID + "-" + input.Version
	_, err := dbos.RunWorkflow(d.dbosCtx, d.workflowFn, input, dbos.WithWorkflowID(id))
	return err
}

func (d *EnvDeployer) deployWorkflow(dctx dbos.DBOSContext, input DeployInput) (DeployResult, error) {
	result, err := dbos.RunAsStep(dctx, func(c context.Context) (DeployResult, error) {
		return d.deployEnv(c, input)
	})
	if err != nil {
		// Record the failure in its own checkpointed step so the console sees it
		// even if the workflow is not retried.
		_, _ = dbos.RunAsStep(dctx, func(c context.Context) (string, error) {
			return "", d.markFailed(c, input, err)
		})
		return DeployResult{}, err
	}
	if _, err := dbos.RunAsStep(dctx, func(c context.Context) (string, error) {
		return "", d.markDeployed(c, input)
	}); err != nil {
		return DeployResult{}, err
	}
	return result, nil
}

// deployEnv resolves the env's running context, reads the custodied k3s token,
// materializes a kube-context that addresses the cluster, and helm-installs the
// runtime chart at the env's version into the per-env namespace. The token
// never leaves this step (it is not part of any DBOS step-output checkpoint).
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

	kubeContext := strings.TrimSpace(ctxRow.KubernetesContext)
	if kubeContext == "" {
		kubeContext = strings.TrimSpace(ctxRow.Name)
	}
	if err := d.writeKubeContext(kubeContext, ctxRow.PublicIP, token); err != nil {
		return DeployResult{}, fmt.Errorf("configure kube context %q: %w", kubeContext, err)
	}

	namespace := eruncommon.KubernetesNamespaceName(tenant.Name, env.Name)
	release := eruncommon.RuntimeReleaseName(tenant.Name)
	spec := d.helmDeploySpec(input.Version, tenant.Name, env.Name, ctxRow, kubeContext, namespace, release)

	ectx := eruncommon.Context{
		Logger: eruncommon.NewLoggerWithWriters(eruncommon.VerbosityInfo, io.Discard, io.Discard),
		DryRun: false,
		Stdout: io.Discard,
		Stderr: io.Discard,
	}
	if err := eruncommon.RunHelmDeploy(ectx, spec, d.deployer); err != nil {
		return DeployResult{}, fmt.Errorf("deploy runtime chart: %w", err)
	}
	return DeployResult{Namespace: namespace, Release: release, Version: input.Version, Status: statusDeployed}, nil
}

// helmDeploySpec assembles the runtime-chart deploy plan from the DB-sourced
// env + context. It mirrors the published-devops-chart spec the CLI/MCP build
// (resolvePublishedDevopsDeploySpec) without the on-disk erun config tree: the
// chart is the published OCI ref pinned to the env's version, the release is
// per-tenant, and the namespace is per-(tenant, env). Cloudflare/MCP-auth
// fields stay empty so the path is the legacy no-secret deploy.
func (d *EnvDeployer) helmDeploySpec(version, tenantName, envName string, ctxRow model.Context, kubeContext, namespace, release string) eruncommon.HelmDeploySpec {
	chartPath := d.chartPathOverride
	if chartPath == "" {
		chartPath = eruncommon.PublishedDevopsChartOCIRepo(d.runtimeRegistry) + "/" + eruncommon.DevopsComponentName
	}
	spec := eruncommon.HelmDeploySpec{
		ReleaseName:        release,
		ChartPath:          chartPath,
		Version:            version,
		Tenant:             tenantName,
		Environment:        envName,
		Namespace:          namespace,
		KubernetesContext:  kubeContext,
		WorktreeStorage:    "none",
		ManagedCloud:       true,
		CloudContextName:   ctxRow.Name,
		CloudProvider:      ctxRow.Provider,
		CloudProviderAlias: ctxRow.CloudProviderAlias,
		CloudRegion:        ctxRow.Region,
		CloudInstanceID:    ctxRow.InstanceID,
		ContainerRegistry:  d.runtimeRegistry,
		RuntimeRegistry:    d.runtimeRegistry,
		Timeout:            eruncommon.DefaultHelmDeploymentTimeout,
	}
	if d.imageOverride != "" {
		spec.ImageOverrides = map[string]string{eruncommon.DevopsComponentName: d.imageOverride}
	}
	return spec
}

// writeKubeContext mirrors eruncommon.configureCloudKubeContext exactly: it
// upserts a cluster, a bearer-token user, and a context — all named kubeContext
// — into the kubeconfig the helm / kubectl subprocesses read (the ambient
// $KUBECONFIG or ~/.kube/config). The server is https://<publicIp>:6443 with
// TLS verification skipped (k3s serves a self-signed cert); auth is the k3s
// admin bearer token. The writes are serialized so concurrent deploys do not
// corrupt the shared kubeconfig file.
func (d *EnvDeployer) writeKubeContext(kubeContext, publicIP, token string) error {
	server := "https://" + publicIP + ":6443"
	d.kubeMu.Lock()
	defer d.kubeMu.Unlock()
	steps := [][]string{
		{"config", "set-cluster", kubeContext, "--server", server, "--insecure-skip-tls-verify=true"},
		{"config", "set-credentials", kubeContext, "--token", token},
		{"config", "set-context", kubeContext, "--cluster", kubeContext, "--user", kubeContext},
	}
	for _, args := range steps {
		// CombinedOutput, not the args, is surfaced on error: args[1] carries the
		// token for set-credentials, so the error names only the subcommand and
		// kubectl's own message (which never echoes the token).
		if output, err := eruncommon.Command("kubectl", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("kubectl config %s: %w: %s", args[1], err, strings.TrimSpace(string(output)))
		}
	}
	return nil
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
