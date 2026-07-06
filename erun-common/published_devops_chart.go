package eruncommon

import (
	"context"
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

// resolvePublishedRuntimeChartReference prefers the tenant's own published
// <tenant>-devops chart (typically a thin umbrella wrapping erun-devops, per the
// erun-build-env skill) and falls back to the canonical charts/erun-devops when
// the tenant publishes none — the published analogue of the local
// runtimeComponentNames order. The tenant chart is used
// only when it actually publishes the deploy version, probed against the chart
// repo (authenticated like every registry read); any probe failure or miss
// falls back, so an offline resolve and a tenant that rides the shared chart
// both behave exactly as before. The erun tenant's release name is erun-devops,
// so no probe runs for it and its resolution is unchanged.
func resolvePublishedRuntimeChartReference(ctx context.Context, containerRegistry, tenant, version string) string {
	tenantChart := RuntimeReleaseName(tenant)
	if tenantChart != DevopsComponentName && publishedChartHasVersion(ctx, containerRegistry, tenantChart, version) {
		return PublishedDevopsChartOCIRepo(containerRegistry) + "/" + tenantChart
	}
	return publishedDevopsChartReference(containerRegistry)
}

// ensureTenantChartsPublished verifies, before any spec is built, that a
// tenant-artifact deploy's charts all exist at the resolved version. A tenant
// runs its runtime and components together on its own version line (independent
// of the shared erun-devops line), so once a deploy rolls out the tenant's own
// component charts the tenant runtime chart (<tenant>-devops) and every selected
// component chart must be published at the version. Failing here keeps the deploy
// from silently installing the vanilla erun-devops runtime via the chart fallback,
// or half-applying before a missing chart aborts the rollout.
func ensureTenantChartsPublished(ctx Context, target OpenResult, versionOverride string, runtimeSelected bool, components []string) error {
	version := strings.TrimSpace(versionOverride)
	if version == "" {
		version = strings.TrimSpace(target.EnvConfig.RuntimeVersion)
	}
	if version == "" {
		ctx.Trace("deploy: no version resolved; cannot verify the tenant's charts are published")
		return fmt.Errorf("version is required to deploy the tenant's charts: pass --version or persist runtimeversion in the env config")
	}
	registry := publishedDevopsChartRegistry(target)

	required := make([]string, 0, len(components)+1)
	runtimeChart := RuntimeReleaseName(target.Tenant)
	if runtimeSelected && runtimeChart != DevopsComponentName {
		required = append(required, runtimeChart)
	}
	required = append(required, components...)

	ctx.Trace("deploy: deploying tenant artifacts; verifying charts published at " + version + " in " + registry + ": " + strings.Join(required, ", "))

	missing := make([]string, 0, len(required))
	for _, chart := range required {
		if !publishedChartHasVersion(context.Background(), registry, chart, version) {
			missing = append(missing, chart)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("deploy rolls out the tenant's own artifacts, which run on the tenant's version line, but these charts are not published at version %s in %s: %s; `erun push --version %s` (or `erun release`) publishes the tenant's runtime and component charts together, so publish the missing chart(s) then deploy", version, registry, strings.Join(missing, ", "), version)
	}
	return nil
}

func publishedChartHasVersion(ctx context.Context, containerRegistry, chartName, version string) bool {
	if override, ok := os.LookupEnv(publishedChartProbeOverrideEnv); ok {
		return publishedChartOverrideHasVersion(override, chartName, version)
	}
	versions, err := ResolveRuntimeImageRegistryVersions(ctx, containerRegistry, "charts/"+chartName)
	if err != nil {
		return false
	}
	return versions.HasVersion(version)
}

// publishedChartProbeOverrideEnv is a test-only seam that answers the "does
// charts/<name>:<version> exist?" registry probe from a static list instead of
// a live registry read, so integration goldens never depend on a real
// registry's contents. Not a production knob: when the variable is unset the
// probe performs the real authenticated registry read. Format:
// comma-separated "<chart>:<version>" entries treated as published; anything
// absent (including an empty value) is treated as unpublished.
const publishedChartProbeOverrideEnv = "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE"

func publishedChartOverrideHasVersion(override, chartName, version string) bool {
	for _, entry := range strings.Split(override, ",") {
		name, ver, ok := strings.Cut(strings.TrimSpace(entry), ":")
		if !ok {
			continue
		}
		if strings.TrimSpace(name) == chartName && strings.TrimSpace(ver) == version {
			return true
		}
	}
	return false
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

// resolvePublishedDevopsDeploySpec builds the deploy spec for an environment
// with no local runtime chart, using the published erun-devops chart pinned to
// the env's runtime version (one version covers both chart and image, published
// together at release). The published chart is the single contract, replacing
// the per-tenant embedded chart copy init once scaffolded, which had drifted
// from canonical.
func resolvePublishedDevopsDeploySpec(ctx Context, target OpenResult, versionOverride string) (DeploySpec, error) {
	return resolvePublishedDevopsDeploySpecWithReason(ctx, target, versionOverride, "no local runtime chart")
}

func resolvePublishedDevopsDeploySpecWithReason(ctx Context, target OpenResult, versionOverride, reason string) (DeploySpec, error) {
	registry := publishedDevopsChartRegistry(target)
	version := strings.TrimSpace(versionOverride)
	if version == "" {
		version = strings.TrimSpace(target.EnvConfig.RuntimeVersion)
	}
	if version == "" {
		// Dry-run contract: the decision that stops the plan must surface as a
		// trace line before the error return.
		ctx.Trace("deploy: " + reason + " and no runtime version resolved; cannot deploy the published " + DevopsComponentName + " chart")
		return DeploySpec{}, fmt.Errorf("runtime version is required to deploy the published %s chart: pass --version or persist runtimeversion in the env config", DevopsComponentName)
	}

	chartReference := resolvePublishedRuntimeChartReference(context.Background(), registry, target.Tenant, version)
	ctx.Trace("deploy: " + reason + "; using published chart " + chartReference + " version " + version)

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
	deployInput.UseHostCredentials = target.EnvConfig.HasAWSCloudAlias()
	deployInput.ContainerRegistry = registry
	if image := resolveRuntimeImageOverride(registry, version, target.EnvConfig.RuntimeImage); image != "" {
		ctx.Trace("deploy: runtime image override " + image + " (imageOverrides." + DevopsComponentName + ")")
		deployInput.ImageOverrides = map[string]string{DevopsComponentName: image}
	}
	// A runtime env that opted into a mutable source worktree clones this repo
	// at the deployed release tag on first boot; resolveWorktreeStorage already
	// put the worktree on a PVC for it.
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

// resolvePublishedComponentDeploySpec builds a deploy spec that installs a
// published platform component chart (erun-backend-*, erun-powerdns, erun-docs)
// by reference — the sourceless analogue of resolvePublishedDevopsDeploySpec.
// The chart installs directly (top-level, not wrapped as a subchart), so erun
// deploy's top-level --set tenant/environment reach it: no local umbrella and
// no repo source are needed. The release is named <tenant>-<component> so it is
// tenant-clear and matches the umbrella (patch-path) naming.
func resolvePublishedComponentDeploySpec(ctx Context, target OpenResult, componentName, versionOverride string) (DeploySpec, error) {
	registry := publishedDevopsChartRegistry(target)
	version := strings.TrimSpace(versionOverride)
	if version == "" {
		version = strings.TrimSpace(target.EnvConfig.RuntimeVersion)
	}
	if version == "" {
		// Dry-run contract: trace the stopping decision before the error return.
		ctx.Trace("deploy: no version resolved for component " + componentName + "; cannot deploy its published chart")
		return DeploySpec{}, fmt.Errorf("version is required to deploy the published %s chart: pass --version or persist runtimeversion in the env config", componentName)
	}

	chartReference := PublishedDevopsChartOCIRepo(registry) + "/" + componentName
	ctx.Trace("deploy: no local chart for " + componentName + "; using published chart " + chartReference + " version " + version)

	deployContext := KubernetesDeployContext{
		ComponentName: componentName,
		ChartPath:     chartReference,
	}
	deployInput, err := newHelmDeploySpecWithValues(target, deployContext, version, "")
	if err != nil {
		return DeploySpec{}, err
	}
	deployInput.ReleaseName = publishedComponentReleaseName(target.Tenant, componentName)
	deployInput.ContainerRegistry = registry
	deployInput.UseHostCredentials = target.EnvConfig.HasAWSCloudAlias()

	return DeploySpec{
		Target:        target,
		DeployContext: deployContext,
		Deploy:        deployInput,
	}, nil
}

// publishedComponentReleaseName maps a published component chart to its release
// name: <tenant>-<component-suffix> (e.g. erun-backend-api → frs-backend-api). A
// chart already named for this tenant (a tenant's own frs-backend-api) is its
// own release name — don't double-prefix it to frs-frs-backend-api.
func publishedComponentReleaseName(tenant, component string) string {
	tenant = strings.TrimSpace(tenant)
	component = strings.TrimSpace(component)
	if tenant != "" && strings.HasPrefix(component, tenant+"-") {
		return component
	}
	suffix := strings.TrimPrefix(component, "erun-")
	return tenant + "-" + suffix
}

// publishedDevopsValuesOverlayPath finds the env's operator values overlay.
// A published chart has no local chart directory to hold the usual
// values.<env>.yaml, so the overlay lives beside the env config instead; its
// absence just means chart defaults and erun's --set list fully describe the
// deploy.
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

// publishedDevopsChartRegistry prefers the env's recorded RuntimeRegistry so a
// reopen keeps addressing the registry the env was deployed from.
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

// PublishedChartNotFoundError reports that the published runtime chart a remote
// deploy resolved to could not be pulled at the resolved version. The usual
// cause is a version whose runtime image was pushed to the registry — so the
// version picker offered it — but whose chart the release flow never published
// (snapshots and unreleased versions are not published as charts), or a chart
// tag that has since been pruned. It replaces helm's opaque chart-pull exit
// status with an actionable message naming the version and registry.
type PublishedChartNotFoundError struct {
	ChartReference string
	Version        string
	Registry       string
	HelmOutput     string
	Err            error
}

func (e *PublishedChartNotFoundError) Error() string {
	msg := "runtime chart " + strings.TrimSpace(e.ChartReference) + " version " + strings.TrimSpace(e.Version) + " could not be pulled"
	if registry := strings.TrimSpace(e.Registry); registry != "" {
		msg += " from " + registry
	}
	msg += ": that version has no published chart in the registry. " +
		"`erun push` publishes a version's image and chart together, so a version is deployable only after it is pushed — " +
		"run `erun push --version " + strings.TrimSpace(e.Version) + "` (or `erun release` for a release version), then deploy."
	if out := strings.TrimSpace(e.HelmOutput); out != "" {
		msg += "\n" + out
	}
	return msg
}

func (e *PublishedChartNotFoundError) Unwrap() error { return e.Err }

// resolveRuntimeImageOverride normalizes a custom runtime image. A reference
// that already pins a tag or digest is used verbatim; a tagless one is pinned
// to the env's runtime version, because a bare registry path would otherwise
// default to :latest — a tag the release flow never publishes (ImagePullBackOff).
func resolveRuntimeImageOverride(registry, version, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if imageReferenceHasTagOrDigest(raw) {
		return raw
	}
	if !strings.Contains(raw, "/") {
		raw = strings.TrimSpace(registry) + "/" + raw
	}
	return raw + ":" + strings.TrimSpace(version)
}

// imageReferenceHasTagOrDigest reports whether an image reference already pins a
// tag or digest. A ":" in the registry host is a port, not a tag (e.g.
// localhost:5000/img), so only the segment after the last "/" is inspected.
func imageReferenceHasTagOrDigest(ref string) bool {
	if strings.Contains(ref, "@") {
		return true
	}
	lastSegment := ref[strings.LastIndex(ref, "/")+1:]
	return strings.Contains(lastSegment, ":")
}
