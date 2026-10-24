package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/helm"
	registrypkg "github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// PublishedChartProbeOverrideEnv is a test-only seam answering the chart probe
// from a static list: comma-separated "<chart>:<version>" (any registry) or
// "<registry>/<chart>:<version>"; "*" matches every version. Unset means a real read.
const PublishedChartProbeOverrideEnv = "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE"

func publishedChartOverrideHasVersion(override, containerRegistry, chartName, version string) bool {
	for _, entry := range strings.Split(override, ",") {
		coordinate, ver, ok := shared.CutLast(strings.TrimSpace(entry), ":")
		ver = strings.TrimSpace(ver)
		if !ok || (ver != "*" && ver != version) {
			continue
		}
		registry, name, qualified := shared.CutLast(strings.TrimSpace(coordinate), "/")
		if !qualified {
			registry, name = "", coordinate
		}
		if name != chartName {
			continue
		}
		if registry == "" || registry == strings.TrimSpace(containerRegistry) {
			return true
		}
	}
	return false
}

// IsOCIChartReference reports whether chartPath is a published oci:// chart rather than a local directory.
func IsOCIChartReference(chartPath string) bool {
	return isOCIChartReference(chartPath)
}

// ResolvePublishedDevopsDeploySpec rebuilds the runtime spec for transports that
// override inputs later; their runtime image is always operator-stated.
func ResolvePublishedDevopsDeploySpec(ctx core.Context, target config.OpenResult, versionOverride string) (DeploySpec, error) {
	return ResolvePublishedDevopsDeploySpecWithOverrides(ctx, target, versionOverride, "", true)
}

// ResolvePublishedDevopsDeploySpecWithOverrides builds the spec for an env with no
// local runtime chart, pinned to the env's runtime version.
func ResolvePublishedDevopsDeploySpecWithOverrides(ctx core.Context, target config.OpenResult, versionOverride, runtimeChartOverride string, runtimeImageExplicit bool) (DeploySpec, error) {
	return ResolvePublishedDevopsDeploySpecWithReason(ctx, target, versionOverride, "no local runtime chart", runtimeChartOverride, runtimeImageExplicit)
}

func ResolvePublishedDevopsDeploySpecWithReason(ctx core.Context, target config.OpenResult, versionOverride, reason, runtimeChartOverride string, runtimeImageExplicit bool) (DeploySpec, error) {
	// Resolve the env's declared registry credential first so private reads are not refused as anonymous.
	registrypkg.ConfigureInPodDeclaredRegistryAuth(ctx, target)
	registry := publishedDevopsChartRegistry(target)
	version := strings.TrimSpace(versionOverride)
	if version == "" {
		version = strings.TrimSpace(target.EnvConfig.RuntimeVersion)
	}
	if version == "" {
		// Dry-run contract: trace the stopping decision before the error return.
		ctx.Trace("deploy: " + reason + " and no runtime version resolved; cannot deploy the published " + config.DevopsComponentName + " chart")
		return DeploySpec{}, fmt.Errorf("runtime version is required to deploy the published %s chart: pass --version or persist runtimeversion in the env config", config.DevopsComponentName)
	}

	chart, err := resolveRuntimeChartCoordinate(ctx, target, registry, version, reason, strings.TrimSpace(runtimeChartOverride) != "")
	if err != nil {
		return DeploySpec{}, err
	}
	traceRuntimeRegistryMemo(ctx, target, registry, chart.registry)

	deployContext := model.KubernetesDeployContext{
		ComponentName: config.DevopsComponentName,
		ChartPath:     chart.reference,
	}
	valuesFilePath := publishedDevopsValuesOverlayPath(ctx, target)
	deployInput, err := newHelmDeploySpecWithValues(target, deployContext, version, valuesFilePath)
	if err != nil {
		return DeploySpec{}, err
	}
	// helm does not pass top-level --sets into subchart scope, so re-scope them under
	// the wrapped erun-devops.
	deployInput.SubchartKey = publishedUmbrellaSubchartKey(target.Tenant, chart.name)
	deployInput.ChartVersion = chart.version
	deployInput.ChartCandidates = chart.candidates
	if chart.movedPin {
		// Without this the next deploy reads the lagging pin back and never converges.
		deployInput.PersistRuntimeChart = chart.reference + ":" + chart.version
	}
	deployInput.ReleaseName = config.RuntimeReleaseName(target.Tenant)
	deployInput.UseHostCredentials = target.EnvConfig.HasAWSCloudAlias()
	deployInput.ContainerRegistry = registry
	deployInput.RegistryCredentialSecretName = strings.TrimSpace(target.EnvConfig.RegistryCredentialSecretName)
	deployInput.PlatformAliasSecretName = strings.TrimSpace(target.EnvConfig.PlatformAliasSecretName)
	deployInput.RuntimeChartRegistry = chart.registry
	image, persistImage := resolveDeployRuntimeImage(ctx, target, registry, version, chart.name, chart.version, runtimeChartOverride, runtimeImageExplicit)
	if image != "" {
		deployInput.ImageOverrides = map[string]string{config.DevopsComponentName: image}
		deployInput.ResolvedRuntimeImage = image
	} else {
		// No override applies, so the chart's stock default image is fully known; record it.
		deployInput.ResolvedRuntimeImage = registry + "/" + config.DefaultRuntimeImageName + ":" + deployInput.resolvedChartVersion()
	}
	deployInput.PersistRuntimeImage = persistImage
	explicitLineChange := runtimeImageExplicit || strings.TrimSpace(runtimeChartOverride) != ""
	if err := guardRuntimeCoordinate(ctx, target, chart, runtimeChartOverride, deployInput, explicitLineChange); err != nil {
		return DeploySpec{}, err
	}
	if target.EnvConfig.MountsRuntimeSource() {
		deployInput.RepoURL = strings.TrimSpace(target.EnvConfig.RepoURL)
		deployInput.RepoRef = "v" + version
		ctx.Trace("deploy: mounting mutable source " + deployInput.RepoURL + " at " + deployInput.RepoRef + " on a PVC worktree")
	}

	return DeploySpec{
		Target:        target,
		DeployContext: deployContext,
		Deploy:        deployInput,
	}, nil
}

// resolvePublishedComponentDeploySpec installs a published component chart by
// reference; a tenant umbrella gets its --sets re-scoped into the wrapped subchart.
func resolvePublishedComponentDeploySpec(ctx core.Context, target config.OpenResult, componentName, versionOverride string) (DeploySpec, error) {
	registry := publishedTenantComponentChartRegistry(target)
	version := strings.TrimSpace(versionOverride)
	if version == "" {
		version = strings.TrimSpace(target.EnvConfig.RuntimeVersion)
	}
	if version == "" {
		ctx.Trace("deploy: no version resolved for component " + componentName + "; cannot deploy its published chart")
		return DeploySpec{}, fmt.Errorf("version is required to deploy the published %s chart: pass --version or persist runtimeversion in the env config", componentName)
	}

	chartReference := helm.PublishedDevopsChartOCIRepo(registry) + "/" + componentName
	ctx.Trace("deploy: no local chart for " + componentName + "; using published chart " + chartReference + " version " + version)

	deployContext := model.KubernetesDeployContext{
		ComponentName: componentName,
		ChartPath:     chartReference,
	}
	deployInput, err := newHelmDeploySpecWithValues(target, deployContext, version, "")
	if err != nil {
		return DeploySpec{}, err
	}
	deployInput.SubchartKey = publishedUmbrellaSubchartKey(target.Tenant, componentName)
	deployInput.ReleaseName = publishedComponentReleaseName(target.Tenant, componentName)
	deployInput.ContainerRegistry = registry
	deployInput.UseHostCredentials = target.EnvConfig.HasAWSCloudAlias()

	return DeploySpec{
		Target:        target,
		DeployContext: deployContext,
		Deploy:        deployInput,
	}, nil
}

// publishedComponentReleaseName maps a chart to <tenant>-<suffix> without
// double-prefixing a chart already named for the tenant.
func publishedComponentReleaseName(tenant, component string) string {
	tenant = strings.TrimSpace(tenant)
	component = strings.TrimSpace(component)
	if tenant != "" && strings.HasPrefix(component, tenant+"-") {
		return component
	}
	suffix := strings.TrimPrefix(component, "erun-")
	return tenant + "-" + suffix
}

// A published chart has no local directory for values.<env>.yaml, so the overlay
// lives beside the env config.
func publishedDevopsValuesOverlayPath(ctx core.Context, target config.OpenResult) string {
	configDir, err := config.ERunConfigDir()
	if err != nil {
		return ""
	}
	overlayPath := filepath.Join(configDir, target.Tenant, target.Environment, "values.yaml")
	if _, err := os.Stat(overlayPath); err != nil {
		return ""
	}
	ctx.Trace("deploy: applying values overlay " + overlayPath)
	return overlayPath
}

func publishedDevopsChartRegistry(target config.OpenResult) string {
	if registry := strings.TrimSpace(target.EnvConfig.RuntimeRegistry); registry != "" {
		return registry
	}
	// A --cluster-registry env's in-cluster registry never holds the platform chart, so
	// its chart comes from the runtime image's registry; other envs ignore runtime-image
	// overrides here.
	if isClusterRegistryEnv(target) {
		if registry := config.RuntimeImageRegistry(target.EnvConfig.RuntimeImage); registry != "" {
			return registry
		}
	}
	if registry, ok := target.EnvConfig.ContainerRegistries.DeployRegistry(); ok {
		return registry
	}
	if registry := resolveProjectContainerRegistry(target.RepoPath, target.Environment); registry != "" {
		return registry
	}
	return config.DefaultContainerRegistry
}

// resolveRuntimeRegistry is projected as RUNTIME_REGISTRY, with the same precedence
// as publishedDevopsChartRegistry.
func resolveRuntimeRegistry(target config.OpenResult) string {
	if r := strings.TrimSpace(target.EnvConfig.RuntimeRegistry); r != "" {
		return r
	}
	if isClusterRegistryEnv(target) {
		return config.RuntimeImageRegistry(target.EnvConfig.RuntimeImage)
	}
	return ""
}

// isClusterRegistryEnv matches both an unresolved cluster: entry and one already
// concretized onto ClusterPullRegistry.
func isClusterRegistryEnv(target config.OpenResult) bool {
	return target.EnvConfig.ContainerRegistries.HasClusterEntry() ||
		strings.TrimSpace(target.ClusterPullRegistry) != ""
}

// RuntimeChartConfirmationError reports that no runtime chart candidate was confirmed
// at the version; an unconfirmed coordinate is never substituted.
type RuntimeChartConfirmationError struct {
	Version      string
	Candidates   []string
	Inconclusive bool
}

func (e *RuntimeChartConfirmationError) Error() string {
	version := strings.TrimSpace(e.Version)
	if e.Inconclusive {
		return "deploy could not confirm a runtime chart at version " + version + " at any coordinate probed — " +
			strings.Join(e.Candidates, "; ") + ". At least one registry read did not return a definitive answer, " +
			"so deploy refuses to guess rather than install a coordinate it has not confirmed; " +
			"check registry credentials/connectivity and retry."
	}
	return "no runtime chart is published at version " + version + " at any coordinate deploy probed — " +
		strings.Join(e.Candidates, "; ") + ". `erun push --version " + version + "` (or `erun build --release`) publishes a " +
		"version's runtime chart, so deploy is refusing rather than installing a coordinate that cannot exist; " +
		"publish the version, or name a chart explicitly with `runtimechart` in the env config (or `--runtime-chart <ref>` for one deploy)."
}

// PublishedChartNotFoundError reports a by-reference chart that could not be
// pulled, with the coordinates probed, since the cause is usually where it looked.
type PublishedChartNotFoundError struct {
	ChartReference string
	Version        string
	Registry       string
	// Candidates is empty for a component chart, which has no ladder.
	Candidates []string
	// TenantChart is empty when the tenant rides the shared chart.
	TenantChart string
	HelmOutput  string
	Err         error
}

func (e *PublishedChartNotFoundError) Error() string {
	version := strings.TrimSpace(e.Version)
	msg := "runtime chart " + strings.TrimSpace(e.ChartReference) + " version " + version + " could not be pulled"
	if registry := strings.TrimSpace(e.Registry); registry != "" {
		msg += " from " + registry
	}
	if len(e.Candidates) == 0 {
		msg += ": that version has no published chart in the registry. " +
			"`erun push` publishes a version's image and chart together, so a version is deployable only after it is pushed — " +
			"run `erun push --version " + version + "` (or `erun build --release` for a release version), then deploy."
		return msg + helmOutputSuffix(e.HelmOutput)
	}
	msg += ": no chart is published at " + version + " at any coordinate the deploy probed — " + strings.Join(e.Candidates, ", ") + ". " +
		"The " + config.DevopsComponentName + " platform chart is published only beside the runtime image erun releases, so a registry holding just this project's own images has it at no version: " +
		"point the environment at the registry that does, with `erun init <tenant> <env> --runtime-registry <registry>`, which persists it as the env's runtimeregistry and redeploys."
	if tenantChart := strings.TrimSpace(e.TenantChart); tenantChart != "" {
		msg += " If this project publishes its own " + tenantChart + " umbrella instead, publish it at this version from the project that owns that chart — `erun push --version " + version + "` (or `erun build --release`) — then deploy."
	}
	msg += " If the environment rides a chart on another line entirely, state it outright: `runtimechart` in the env config (the desktop's Runtime tab, \"Runtime chart\") or `--runtime-chart <ref>` for one deploy, and the version keeps naming the image."
	return msg + helmOutputSuffix(e.HelmOutput)
}

func helmOutputSuffix(helmOutput string) string {
	if out := strings.TrimSpace(helmOutput); out != "" {
		return "\n" + out
	}
	return ""
}

func (e *PublishedChartNotFoundError) Unwrap() error { return e.Err }
