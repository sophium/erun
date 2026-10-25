package published

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
	"github.com/sophium/erun/erun-common/infra/helm"
	registrypkg "github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartref"
	"github.com/sophium/erun/erun-common/primitive/deploy/helmspec"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerregistry"
)

// PublishedChartProbeOverrideEnv is a test-only seam answering the chart probe
// from a static list: comma-separated "<chart>:<version>" (any registry) or
// "<registry>/<chart>:<version>"; "*" matches every version. Unset means a real read.
const PublishedChartProbeOverrideEnv = "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE"

func publishedChartOverrideHasVersion(override, containerRegistry, chartName, version string) bool {
	for _, entry := range strings.Split(override, ",") {
		coordinate, ver, ok := dockerregistry.CutLast(strings.TrimSpace(entry), ":")
		ver = strings.TrimSpace(ver)
		if !ok || (ver != "*" && ver != version) {
			continue
		}
		registry, name, qualified := dockerregistry.CutLast(strings.TrimSpace(coordinate), "/")
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
	return chartref.IsOCIChartReference(chartPath)
}

// ResolvePublishedDevopsDeploySpec rebuilds the runtime spec for transports that
// override inputs later; their runtime image is always operator-stated.
func ResolvePublishedDevopsDeploySpec(ctx core.Context, target config.OpenResult, versionOverride string) (helmdeploy.DeploySpec, error) {
	return ResolvePublishedDevopsDeploySpecWithOverrides(ctx, target, versionOverride, "", true)
}

// ResolvePublishedDevopsDeploySpecWithOverrides builds the spec for an env with no
// local runtime chart, pinned to the env's runtime version.
func ResolvePublishedDevopsDeploySpecWithOverrides(ctx core.Context, target config.OpenResult, versionOverride, runtimeChartOverride string, runtimeImageExplicit bool) (helmdeploy.DeploySpec, error) {
	return ResolvePublishedDevopsDeploySpecWithReason(ctx, target, versionOverride, "no local runtime chart", runtimeChartOverride, runtimeImageExplicit)
}

func ResolvePublishedDevopsDeploySpecWithReason(ctx core.Context, target config.OpenResult, versionOverride, reason, runtimeChartOverride string, runtimeImageExplicit bool) (helmdeploy.DeploySpec, error) {
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
		return helmdeploy.DeploySpec{}, fmt.Errorf("runtime version is required to deploy the published %s chart: pass --version or persist runtimeversion in the env config", config.DevopsComponentName)
	}

	chart, err := resolveRuntimeChartCoordinate(ctx, target, registry, version, reason, strings.TrimSpace(runtimeChartOverride) != "")
	if err != nil {
		return helmdeploy.DeploySpec{}, err
	}
	traceRuntimeRegistryMemo(ctx, target, registry, chart.registry)

	deployContext := model.KubernetesDeployContext{
		ComponentName: config.DevopsComponentName,
		ChartPath:     chart.reference,
	}
	valuesFilePath := publishedDevopsValuesOverlayPath(ctx, target)
	deployInput, err := helmspec.NewHelmDeploySpecWithValues(target, deployContext, version, valuesFilePath)
	if err != nil {
		return helmdeploy.DeploySpec{}, err
	}
	// helm does not pass top-level --sets into subchart scope, so re-scope them under
	// the wrapped erun-devops.
	deployInput.SubchartKey = PublishedUmbrellaSubchartKey(target.Tenant, chart.name)
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
		deployInput.ResolvedRuntimeImage = registry + "/" + config.DefaultRuntimeImageName + ":" + chartref.ResolvedChartVersion(deployInput)
	}
	deployInput.PersistRuntimeImage = persistImage
	explicitLineChange := runtimeImageExplicit || strings.TrimSpace(runtimeChartOverride) != ""
	if err := guardRuntimeCoordinate(ctx, target, chart, runtimeChartOverride, deployInput, explicitLineChange); err != nil {
		return helmdeploy.DeploySpec{}, err
	}
	if target.EnvConfig.MountsRuntimeSource() {
		deployInput.RepoURL = strings.TrimSpace(target.EnvConfig.RepoURL)
		deployInput.RepoRef = "v" + version
		ctx.Trace("deploy: mounting mutable source " + deployInput.RepoURL + " at " + deployInput.RepoRef + " on a PVC worktree")
	}

	return helmdeploy.DeploySpec{
		Target:        target,
		DeployContext: deployContext,
		Deploy:        deployInput,
	}, nil
}

// ResolvePublishedComponentDeploySpec installs a published component chart by
// reference; a tenant umbrella gets its --sets re-scoped into the wrapped subchart.
func ResolvePublishedComponentDeploySpec(ctx core.Context, target config.OpenResult, componentName, versionOverride string) (helmdeploy.DeploySpec, error) {
	registry := publishedTenantComponentChartRegistry(target)
	version := strings.TrimSpace(versionOverride)
	if version == "" {
		version = strings.TrimSpace(target.EnvConfig.RuntimeVersion)
	}
	if version == "" {
		ctx.Trace("deploy: no version resolved for component " + componentName + "; cannot deploy its published chart")
		return helmdeploy.DeploySpec{}, fmt.Errorf("version is required to deploy the published %s chart: pass --version or persist runtimeversion in the env config", componentName)
	}

	chartReference := helm.PublishedDevopsChartOCIRepo(registry) + "/" + componentName
	ctx.Trace("deploy: no local chart for " + componentName + "; using published chart " + chartReference + " version " + version)

	deployContext := model.KubernetesDeployContext{
		ComponentName: componentName,
		ChartPath:     chartReference,
	}
	deployInput, err := helmspec.NewHelmDeploySpecWithValues(target, deployContext, version, "")
	if err != nil {
		return helmdeploy.DeploySpec{}, err
	}
	deployInput.SubchartKey = PublishedUmbrellaSubchartKey(target.Tenant, componentName)
	deployInput.ReleaseName = publishedComponentReleaseName(target.Tenant, componentName)
	deployInput.ContainerRegistry = registry
	deployInput.UseHostCredentials = target.EnvConfig.HasAWSCloudAlias()

	return helmdeploy.DeploySpec{
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
	if helmspec.IsClusterRegistryEnv(target) {
		if registry := config.RuntimeImageRegistry(target.EnvConfig.RuntimeImage); registry != "" {
			return registry
		}
	}
	if registry, ok := target.EnvConfig.ContainerRegistries.DeployRegistry(); ok {
		return registry
	}
	if registry := helmspec.ResolveProjectContainerRegistry(target.RepoPath, target.Environment); registry != "" {
		return registry
	}
	return config.DefaultContainerRegistry
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
