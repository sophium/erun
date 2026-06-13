package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PublishedDevopsChartOCIRepo is the OCI repository the release flow pushes
// the canonical runtime chart to. The "/charts" suffix keeps the chart's tag
// space separate from the image repository of the same name
// (<registry>/erun-devops), which holds the runtime image tags.
func PublishedDevopsChartOCIRepo(containerRegistry string) string {
	return "oci://" + strings.TrimSpace(containerRegistry) + "/charts"
}

func publishedDevopsChartReference(containerRegistry string) string {
	return PublishedDevopsChartOCIRepo(containerRegistry) + "/" + DevopsComponentName
}

// IsOCIChartReference reports whether the chart path addresses a published
// OCI chart (oci://<registry>/charts/<name>) rather than a local chart
// directory. Transports use it to tell a published-chart runtime spec from a
// repo-local one.
func IsOCIChartReference(chartPath string) bool {
	return isOCIChartReference(chartPath)
}

// ResolvePublishedDevopsDeploySpec rebuilds the runtime deploy spec against
// the published chart for transport flows that override inputs after the
// initial resolution (e.g. `erun open --runtime-image`).
func ResolvePublishedDevopsDeploySpec(ctx Context, target OpenResult, versionOverride string) (DeploySpec, error) {
	return resolvePublishedDevopsDeploySpec(ctx, target, versionOverride)
}

// resolvePublishedDevopsDeploySpec builds the runtime deploy spec for an
// environment with no local runtime chart: the published erun-devops chart,
// addressed by OCI reference and pinned to the env's runtime version (one
// version covers chart and image — they are published together at release).
// The env's RuntimeRegistry provenance wins over the published default so a
// reopen keeps addressing the registry the env was deployed from (#363); a
// custom EnvConfig.RuntimeImage rides in as imageOverrides.erun-devops.
//
// This replaces the embedded default-devops-chart copy that init used to
// scaffold per tenant — the copy had already drifted from the canonical
// chart (#510); the published chart is the single contract (#505).
func resolvePublishedDevopsDeploySpec(ctx Context, target OpenResult, versionOverride string) (DeploySpec, error) {
	registry := publishedDevopsChartRegistry(target)
	version := strings.TrimSpace(versionOverride)
	if version == "" {
		version = strings.TrimSpace(target.EnvConfig.RuntimeVersion)
	}
	if version == "" {
		return DeploySpec{}, fmt.Errorf("runtime version is required to deploy the published %s chart: pass --version or persist runtimeversion in the env config", DevopsComponentName)
	}

	chartReference := publishedDevopsChartReference(registry)
	ctx.Trace("deploy: no local runtime chart; using published chart " + chartReference + " version " + version)

	deployContext := KubernetesDeployContext{
		ComponentName: DevopsComponentName,
		ChartPath:     chartReference,
	}
	valuesFilePath := publishedDevopsValuesOverlayPath(ctx, target)
	deployInput, err := newHelmDeploySpecWithValues(target, deployContext, version, valuesFilePath)
	if err != nil {
		return DeploySpec{}, err
	}
	deployInput.ReleaseName = RuntimeReleaseName(target.Tenant)
	deployInput.UseHostCredentials = target.EnvConfig.RemoteHostCredentials
	deployInput.ContainerRegistry = registry
	if image := resolveRuntimeImageOverride(registry, version, target.EnvConfig.RuntimeImage); image != "" {
		ctx.Trace("deploy: runtime image override " + image + " (imageOverrides." + DevopsComponentName + ")")
		deployInput.ImageOverrides = map[string]string{DevopsComponentName: image}
	}

	return DeploySpec{
		Target:        target,
		DeployContext: deployContext,
		Deploy:        deployInput,
	}, nil
}

// publishedDevopsValuesOverlayPath looks for the env's operator values
// overlay next to its config (<ERunConfigDir>/<tenant>/<environment>/
// values.yaml). Repo-local charts carry their overlay as
// values.<env>.yaml in the chart directory; a published chart has no local
// chart directory, so the env config dir is the overlay's home. Absent file
// means no -f flag — the published chart's defaults plus erun's --set list
// fully describe the deploy.
func publishedDevopsValuesOverlayPath(ctx Context, target OpenResult) string {
	configDir, err := ERunConfigDir()
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

// publishedDevopsChartRegistry picks the registry the published chart (and
// default image) is pulled from: the env's recorded RuntimeRegistry
// provenance, then the DEPLOY-marked registry of the project's registry list,
// then the public default.
func publishedDevopsChartRegistry(target OpenResult) string {
	if registry := strings.TrimSpace(target.EnvConfig.RuntimeRegistry); registry != "" {
		return registry
	}
	if registry, ok := target.EnvConfig.ContainerRegistries.DeployRegistry(); ok {
		return registry
	}
	if registry := resolveProjectContainerRegistry(target.RepoPath, target.Environment); registry != "" {
		return registry
	}
	return DefaultContainerRegistry
}

// resolveRuntimeImageOverride normalizes a custom runtime image: a full
// reference (anything carrying a registry path or tag) is used verbatim; a
// bare image name — the historical `--runtime-image <name>` shape — resolves
// against the env's registry and runtime version.
func resolveRuntimeImageOverride(registry, version, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.ContainsAny(raw, "/:") {
		return raw
	}
	return strings.TrimSpace(registry) + "/" + raw + ":" + strings.TrimSpace(version)
}
