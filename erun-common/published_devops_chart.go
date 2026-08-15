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

// runtimeChartCandidate is one coordinate the published-runtime-chart ladder
// probes: a chart name, a registry it could be published in, and why that
// registry is a place to look. The reason travels with the candidate so both
// the resolve trace and the not-found error can say what was tried and why.
type runtimeChartCandidate struct {
	registry string
	chart    string
	why      string
}

func (c runtimeChartCandidate) reference() string {
	return PublishedDevopsChartOCIRepo(c.registry) + "/" + c.chart
}

func (c runtimeChartCandidate) describe() string {
	return strings.TrimSpace(c.registry) + "/charts/" + c.chart + " (" + c.why + ")"
}

// runtimeChartCandidates orders every place a by-reference runtime chart can be.
// The tenant's own umbrella comes first, so a tenant that publishes its own
// artifacts still resolves to them. The shared platform chart follows in the
// same registry, then in the registry the runtime image comes from: erun
// publishes charts/erun-devops only where it releases, so a deploy registry that
// holds nothing but this project's own app images — its own ECR, or the
// in-cluster erun-registry — never has it, and stopping at the deploy registry
// left such an environment undeployable at every version.
func runtimeChartCandidates(target OpenResult, chartRegistry string) []runtimeChartCandidate {
	chartRegistry = strings.TrimSpace(chartRegistry)
	candidates := make([]runtimeChartCandidate, 0, 3)
	if tenantChart := RuntimeReleaseName(target.Tenant); tenantChart != DevopsComponentName {
		candidates = append(candidates, runtimeChartCandidate{chartRegistry, tenantChart, "the tenant's own umbrella"})
	}
	candidates = append(candidates, runtimeChartCandidate{chartRegistry, DevopsComponentName, "the shared platform chart"})
	platformRegistry, why := platformChartRegistry(target)
	if platformRegistry != "" && platformRegistry != chartRegistry {
		candidates = append(candidates, runtimeChartCandidate{platformRegistry, DevopsComponentName, why})
	}
	return candidates
}

// platformChartRegistry names the registry erun's own artifacts come from for
// this env: the runtime image's registry when the env states one, else erun's
// default. Both are the same claim — the platform chart is published beside the
// platform image, never beside the tenant's app images.
func platformChartRegistry(target OpenResult) (registry, why string) {
	if imageRegistry := runtimeImageRegistry(target.EnvConfig.RuntimeImage); imageRegistry != "" {
		return imageRegistry, "the shared platform chart in the runtime image's registry"
	}
	return DefaultContainerRegistry, "the shared platform chart in erun's own registry"
}

// resolvePublishedRuntimeChartReference walks the candidate ladder and installs
// the first coordinate that publishes the deploy version, probed against the
// chart repo (authenticated like every registry read). Every candidate tried and
// passed over is traced, so a dry-run reader sees the whole search rather than
// only its answer. When nothing answers — an offline resolve, an unreadable
// registry, or a genuinely unpublished version — it falls back to the shared
// chart in the env's own chart registry, which is what resolution always
// produced, and returns the probed coordinates so the chart pull's failure can
// name where erun looked instead of blaming the version.
func resolvePublishedRuntimeChartReference(ctx Context, target OpenResult, chartRegistry, version string) (reference, chartName string, candidates []string) {
	probed := runtimeChartCandidates(target, chartRegistry)
	candidates = make([]string, 0, len(probed))
	for _, candidate := range probed {
		candidates = append(candidates, candidate.describe())
	}
	for _, candidate := range probed {
		if publishedChartHasVersion(context.Background(), candidate.registry, candidate.chart, version) {
			ctx.Trace("deploy: runtime chart " + candidate.chart + " " + version + " found in " + candidate.registry + " (" + candidate.why + ")")
			return candidate.reference(), candidate.chart, candidates
		}
		ctx.Trace("deploy: runtime chart " + candidate.chart + " " + version + " not found in " + candidate.registry + " (" + candidate.why + ")")
	}
	fallback := runtimeChartCandidate{strings.TrimSpace(chartRegistry), DevopsComponentName, "the shared platform chart"}
	ctx.Trace("deploy: no runtime chart candidate published " + version + "; falling back to " + fallback.chart + " in " + fallback.registry)
	return fallback.reference(), fallback.chart, candidates
}

// resolveRuntimeChartCoordinate answers which runtime chart to install, at which
// version. An env that states its chart (EnvConfig.RuntimeChart) is taken at its
// word -- that is the coordinate, and its version, when it carries one, is the
// chart's own rather than the deploy version. Otherwise the chart is looked up
// along the candidate ladder, at the deploy version.
//
// The returned chart version is empty for the looked-up case, meaning "the deploy
// version", so nothing changes for the envs whose chart and image were published
// as a pair.
func resolveRuntimeChartCoordinate(ctx Context, target OpenResult, registry, version, reason string) (reference, chartName, chartVersion string, candidates []string) {
	if named := strings.TrimSpace(target.EnvConfig.RuntimeChart); named != "" {
		reference, chartVersion = splitChartReferenceVersion(named)
		chartName = chartNameFromReference(reference)
		if chartVersion == "" {
			ctx.Trace("deploy: " + reason + "; using the env's runtime chart " + reference + " at the deploy version " + version)
			return reference, chartName, "", nil
		}
		ctx.Trace("deploy: " + reason + "; using the env's runtime chart " + reference + " version " + chartVersion)
		return reference, chartName, chartVersion, nil
	}
	reference, chartName, candidates = resolvePublishedRuntimeChartReference(ctx, target, registry, version)
	ctx.Trace("deploy: " + reason + "; using published chart " + reference + " version " + version)
	return reference, chartName, "", candidates
}

// publishedUmbrellaSubchartKey returns the value-scope key of the canonical
// erun-<base> chart that a tenant's published umbrella wraps as a subchart, or
// "" when the chart is a canonical erun-<base> chart installed directly (no
// wrapper). A tenant that ships its own artifacts publishes <tenant>-<base>
// umbrellas — the runtime <tenant>-devops and each <tenant>-<component> — that
// reference the canonical erun-<base> chart as a subchart (per erun-build-env /
// erun-blueprint-platform: the dependency is named erun-<base>, no alias). helm
// does not pass a by-reference deploy's top-level --set values into subchart
// scope, so deploy nests them under this key (command -> prefixHelmSetKeys) —
// the by-reference analogue of the local runtime umbrella's
// helmChartRuntimeSubchartKey. The erun product tenant's charts ARE the
// canonical charts, installed directly, so they resolve to "".
func publishedUmbrellaSubchartKey(tenant, chartName string) string {
	chartName = strings.TrimSpace(chartName)
	base, ok := strings.CutPrefix(chartName, TenantResourcePrefix(tenant)+"-")
	if !ok {
		return ""
	}
	canonical := canonicalChartPrefix + "-" + base
	if chartName == canonical {
		return ""
	}
	return canonical
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
	// An env that states its runtime chart is not riding a tenant chart at this
	// version, so requiring one here would fail a deploy that is perfectly
	// coherent: the components run on the tenant's line, the runtime chart on the
	// line the env named. Its components are still verified.
	runtimeChartStated := strings.TrimSpace(target.EnvConfig.RuntimeChart) != ""
	switch {
	case runtimeSelected && runtimeChartStated:
		ctx.Trace("deploy: the env states its runtime chart " + strings.TrimSpace(target.EnvConfig.RuntimeChart) + "; verifying only the tenant's component charts at " + version)
	case runtimeSelected && runtimeChart != DevopsComponentName:
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
		return publishedChartOverrideHasVersion(override, containerRegistry, chartName, version)
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
// probe performs the real authenticated registry read. Format: comma-separated
// "<chart>:<version>" entries treated as published in every registry, or
// "<registry>/<chart>:<version>" to publish one only in that registry — which is
// what lets a scenario put the same chart name in one registry and not another,
// the shape the runtime chart ladder walks. Anything absent (including an empty
// value) is treated as unpublished.
const publishedChartProbeOverrideEnv = "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE"

func publishedChartOverrideHasVersion(override, containerRegistry, chartName, version string) bool {
	for _, entry := range strings.Split(override, ",") {
		coordinate, ver, ok := cutLast(strings.TrimSpace(entry), ":")
		if !ok || strings.TrimSpace(ver) != version {
			continue
		}
		registry, name, qualified := cutLast(strings.TrimSpace(coordinate), "/")
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

// cutLast splits around the LAST separator, so a coordinate whose registry
// carries the same separator (a port in localhost:5000, an org path in
// ghcr.io/sophium) still parses.
func cutLast(s, sep string) (before, after string, found bool) {
	index := strings.LastIndex(s, sep)
	if index < 0 {
		return s, "", false
	}
	return s[:index], s[index+len(sep):], true
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

	chartReference, chartName, chartVersion, chartCandidates := resolveRuntimeChartCoordinate(ctx, target, registry, version, reason)

	deployContext := KubernetesDeployContext{
		ComponentName: DevopsComponentName,
		ChartPath:     chartReference,
	}
	valuesFilePath := publishedDevopsValuesOverlayPath(ctx, target)
	deployInput, err := newHelmDeploySpecWithValues(target, deployContext, version, valuesFilePath)
	if err != nil {
		return DeploySpec{}, err
	}
	// A tenant's own <tenant>-devops chart wraps the canonical erun-devops as a
	// subchart; helm won't pass this by-reference deploy's top-level --sets into
	// subchart scope, so re-scope them under erun-devops. Empty (no re-scope) when
	// the tenant rides the shared erun-devops chart.
	deployInput.SubchartKey = publishedUmbrellaSubchartKey(target.Tenant, chartName)
	deployInput.ChartVersion = chartVersion
	deployInput.ChartCandidates = chartCandidates
	deployInput.ReleaseName = RuntimeReleaseName(target.Tenant)
	deployInput.UseHostCredentials = target.EnvConfig.HasAWSCloudAlias()
	deployInput.ContainerRegistry = registry
	if image := resolveDeployRuntimeImage(ctx, target, registry, version, chartName, chartVersion); image != "" {
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
// published platform component chart by reference — the sourceless analogue of
// resolvePublishedDevopsDeploySpec. A canonical erun-<component> chart installs
// directly (top-level), so erun deploy's top-level --set tenant/environment
// reach it. A tenant's own <tenant>-<component> chart is an umbrella wrapping
// the canonical erun-<component> as a subchart, which those top-level --sets do
// not reach; publishedUmbrellaSubchartKey resolves the erun-<component> scope so
// command() re-scopes them and the subchart's required tenant/environment are
// satisfied. The release is named <tenant>-<component> so it is tenant-clear.
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
	// The published erun-devops runtime chart and its platform images (erun-devops,
	// erun-dind, …) are released together to the runtime image's registry
	// (e.g. ghcr.io/sophium). A `--cluster-registry` env's deploy registry is the
	// in-cluster erun-registry that only ever holds the tenant's built app images —
	// never the erun platform chart — so for that env alone the chart must resolve
	// from the runtime image's own registry, or every chart pull fails
	// (ImagePullBackOff at init). A plain env publishes its platform chart to its
	// deploy registry, so its chart follows where charts are published, never a
	// runtime-image override (an image-only concern the chart must not inherit).
	if isClusterRegistryEnv(target) {
		if registry := runtimeImageRegistry(target.EnvConfig.RuntimeImage); registry != "" {
			return registry
		}
	}
	if registry, ok := target.EnvConfig.ContainerRegistries.DeployRegistry(); ok {
		return registry
	}
	if registry := resolveProjectContainerRegistry(target.RepoPath, target.Environment); registry != "" {
		return registry
	}
	return DefaultContainerRegistry
}

// runtimeImageRegistry returns the registry prefix (host and any org path) of a
// runtime image reference, or "" when the reference is a bare image name with no
// registry. The image name and any tag/digest live in the segment after the last
// "/", so everything before it is the registry — e.g. ghcr.io/sophium/erun-devops
// and ghcr.io/sophium/erun-devops:1.0.149 both yield ghcr.io/sophium, while a bare
// "erun-devops" yields "".
func runtimeImageRegistry(runtimeImage string) string {
	ref := strings.TrimSpace(runtimeImage)
	lastSlash := strings.LastIndex(ref, "/")
	if lastSlash < 0 {
		return ""
	}
	return ref[:lastSlash]
}

// resolveRuntimeRegistry is the registry projected into the runtime pod as
// RUNTIME_REGISTRY (nested in-pod image resolution). Prefer the persisted
// runtimeregistry; when it is empty a `--cluster-registry` env falls back to the
// runtime image's own registry — the same precedence publishedDevopsChartRegistry
// uses — so the pod resolves nested platform images from where they are published
// (e.g. ghcr) rather than the in-cluster registry that never held them. A plain
// env projects nothing here: its runtime registry follows the deploy registry the
// chart already renders, never a runtime-image override.
func resolveRuntimeRegistry(target OpenResult) string {
	if r := strings.TrimSpace(target.EnvConfig.RuntimeRegistry); r != "" {
		return r
	}
	if isClusterRegistryEnv(target) {
		return runtimeImageRegistry(target.EnvConfig.RuntimeImage)
	}
	return ""
}

// isClusterRegistryEnv reports whether the deploy target addresses an in-cluster
// (`--cluster-registry`) registry — either still as an unresolved cluster: entry
// (the init path passes it through to the runtime chart) or already concretized to
// its in-cluster pull host on ClusterPullRegistry (the deploy path resolves it up
// front). The erun platform chart and images are never published to that in-cluster
// registry, so only such an env resolves its runtime chart/registry from the runtime
// image's own registry instead of its deploy registry.
func isClusterRegistryEnv(target OpenResult) bool {
	return target.EnvConfig.ContainerRegistries.HasClusterEntry() ||
		strings.TrimSpace(target.ClusterPullRegistry) != ""
}

// PublishedChartNotFoundError reports that the chart a by-reference deploy
// resolved to could not be pulled at the resolved version. For the runtime chart
// it carries the coordinates resolution probed, because the cause is usually
// *where* the deploy looked rather than which version it asked for: erun
// publishes charts/erun-devops only beside the runtime image it releases, so an
// environment whose deploy registry holds nothing but its own app images has no
// platform chart in it at any version. It replaces helm's opaque chart-pull exit
// status with the coordinates tried and the ways out of them.
type PublishedChartNotFoundError struct {
	ChartReference string
	Version        string
	Registry       string
	// Candidates are the registry/chart coordinates the runtime chart ladder
	// probed, in order. Empty for a component chart, which has a single
	// coordinate and no ladder.
	Candidates []string
	// TenantChart is the tenant's own runtime umbrella, named so the message can
	// point at publishing it. Empty when the tenant rides the shared chart.
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
			"run `erun push --version " + version + "` (or `erun release` for a release version), then deploy."
		return msg + helmOutputSuffix(e.HelmOutput)
	}
	msg += ": no chart is published at " + version + " at any coordinate the deploy probed — " + strings.Join(e.Candidates, ", ") + ". " +
		"The " + DevopsComponentName + " platform chart is published only beside the runtime image erun releases, so a registry holding just this project's own images has it at no version: " +
		"point the environment at the registry that does, with `erun init <tenant> <env> --runtime-registry <registry>`, which persists it as the env's runtimeregistry and redeploys."
	if tenantChart := strings.TrimSpace(e.TenantChart); tenantChart != "" {
		msg += " If this project publishes its own " + tenantChart + " umbrella instead, publish it at this version from the project that owns that chart — `erun push --version " + version + "` (or `erun release`) — then deploy."
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

// resolveDeployRuntimeImage resolves the image the runtime pod's erun-devops
// container runs, as the imageOverrides.erun-devops the deploy sets. An explicit
// runtimeimage (the operator's choice) wins, except where it names the stock
// erun-devops image on a deploy that is not on erun's own release line — see
// staleStockRuntimeImageTrace. Otherwise the deploy's own line names the image.
func resolveDeployRuntimeImage(ctx Context, target OpenResult, chartRegistry, version, chartName, chartVersion string) string {
	chartName = strings.TrimSpace(chartName)
	version = strings.TrimSpace(version)
	registry := deployRuntimeImageRegistry(target, chartRegistry)
	if image := resolveRuntimeImageOverride(registry, version, target.EnvConfig.RuntimeImage); image != "" {
		stale := staleStockRuntimeImageTrace(image, chartName, version, strings.TrimSpace(chartVersion))
		if stale == "" {
			ctx.Trace("deploy: runtime image override " + image + " (imageOverrides." + DevopsComponentName + ")")
			return image
		}
		ctx.Trace(stale)
	}
	return defaultDeployRuntimeImage(ctx, registry, version, strings.TrimSpace(target.Tenant), chartName)
}

// staleStockRuntimeImageTrace explains why a runtimeimage naming the stock
// erun-devops image cannot be honored on this deploy, or "" when it can be. Two
// deploys are not on erun's own release line: a tenant umbrella, which publishes
// its own image beside its own chart, and a chart stated at its own version,
// which is the operator saying so outright. Neither line publishes the stock
// image at its version, so a runtimeimage still naming it is a leftover from when
// the env rode the shared chart, and honoring it would pin a tag that never
// existed (ImagePullBackOff).
func staleStockRuntimeImageTrace(image, chartName, version, chartVersion string) string {
	if !runtimeImageIsStockDevops(image) {
		return ""
	}
	switch {
	case chartName != "" && chartName != DevopsComponentName:
		return "deploy: ignoring stale runtimeimage " + image + " on the " + chartName + " umbrella deploy (the stock " + DevopsComponentName + " image is not published on this version line); defaulting to the umbrella's own image"
	case chartVersion != "" && chartVersion != version:
		return "deploy: ignoring stale runtimeimage " + image + " (the env states its runtime chart at " + chartVersion + ", so version " + version + " is on another line and the stock " + DevopsComponentName + " image is not published at it); defaulting to the tenant's own image"
	}
	return ""
}

// defaultDeployRuntimeImage names the image the deploy's own line publishes: the
// umbrella's, when the tenant deploys its own <tenant>-devops chart, else the
// tenant's own <tenant>-devops image, which erun-build-env builds and erun push
// publishes on the tenant's version line. Defaulting to it keeps a deploy with no
// explicit runtimeimage off the stock erun-devops, which carries none of the
// tenant's customizations. The erun product tenant rides the stock image itself,
// so it emits no override and the chart's own default wins.
func defaultDeployRuntimeImage(ctx Context, registry, version, tenant, chartName string) string {
	if chartName != "" && chartName != DevopsComponentName {
		image := registry + "/" + chartName + ":" + version
		ctx.Trace("deploy: defaulting runtime image to the " + chartName + " chart's own image " + image + " (imageOverrides." + DevopsComponentName + ")")
		return image
	}
	tenantImage := RuntimeReleaseName(tenant)
	if tenant == "" || tenantImage == DevopsComponentName {
		return ""
	}
	image := registry + "/" + tenantImage + ":" + version
	ctx.Trace("deploy: defaulting runtime image to the tenant's " + tenantImage + " image " + image + " (imageOverrides." + DevopsComponentName + ")")
	return image
}

// deployRuntimeImageRegistry answers which registry the runtime image is pulled
// from. The chart's registry is not it: an env that states its runtime chart
// points the chart at erun's line on purpose, while its own images stay where it
// builds and publishes them. So the image follows the role that describes the
// cluster's pull, and falls back to the chart's registry only when no entry
// marks one.
func deployRuntimeImageRegistry(target OpenResult, chartRegistry string) string {
	if registry, ok := target.EnvConfig.ContainerRegistries.DeployRegistry(); ok {
		return registry
	}
	return strings.TrimSpace(chartRegistry)
}

// runtimeImageIsStockDevops reports whether a resolved runtime image reference
// names the stock erun-devops image (regardless of registry). A tenant umbrella
// deploy uses it to detect a stale runtimeimage pin left over from the shared
// erun-devops chart, which its own version line never publishes.
func runtimeImageIsStockDevops(image string) bool {
	_, name, _, ok := parseDockerImageReference(image)
	return ok && name == DefaultRuntimeImageName
}

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
