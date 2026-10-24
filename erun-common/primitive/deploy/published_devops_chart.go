package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/helm"
	registrypkg "github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// PublishedChartProbeOverrideEnv is a test-only seam that answers the "does
// charts/<name>:<version> exist?" registry probe from a static list instead of
// a live registry read, so integration goldens never depend on a real
// registry's contents. Not a production knob: when the variable is unset the
// probe performs the real authenticated registry read. Format: comma-separated
// "<chart>:<version>" entries treated as published in every registry, or
// "<registry>/<chart>:<version>" to publish one only in that registry — which is
// what lets a scenario put the same chart name in one registry and not another,
// the shape the runtime chart ladder walks. "*" in place of the version marks a
// chart published at every version, for a scenario that only cares that the
// chart resolves rather than pinning to one version. Anything absent (including
// an empty value) is treated as unpublished.
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

// IsOCIChartReference reports whether the chart path addresses a published
// OCI chart (oci://<registry>/charts/<name>) rather than a local chart
// directory. Transports use it to tell a published-chart runtime spec from a
// repo-local one.
func IsOCIChartReference(chartPath string) bool {
	return isOCIChartReference(chartPath)
}

// ResolvePublishedDevopsDeploySpec rebuilds the runtime deploy spec against
// the published chart for transport flows that override inputs after the
// initial resolution (e.g. `erun open --runtime-image`). Callers on this path
// never carry a --runtime-chart override of their own, and always carry an
// operator-stated runtime image (that is the whole point of the call), so it
// must never be discarded for an inferred one.
func ResolvePublishedDevopsDeploySpec(ctx core.Context, target config.OpenResult, versionOverride string) (DeploySpec, error) {
	return ResolvePublishedDevopsDeploySpecWithOverrides(ctx, target, versionOverride, "", true)
}

// ResolvePublishedDevopsDeploySpecWithOverrides builds the deploy spec for an environment
// with no local runtime chart, using the published erun-devops chart pinned to
// the env's runtime version (one version covers both chart and image, published
// together at release). The published chart is the single contract, replacing
// the per-tenant embedded chart copy init once scaffolded, which had drifted
// from canonical. runtimeChartOverride is the operator's --runtime-chart value
// when one is coming (applyRuntimeChartOverride applies it after this spec is
// built) -- see resolvePublishedRuntimeChartReference's deferToOverride doc.
// runtimeImageExplicit is true when target.EnvConfig.RuntimeImage was set by an
// operator's --runtime-image on this very invocation, rather than merely
// carried over from a previous deploy's persisted memo -- see
// resolveDeployRuntimeImage.
func ResolvePublishedDevopsDeploySpecWithOverrides(ctx core.Context, target config.OpenResult, versionOverride, runtimeChartOverride string, runtimeImageExplicit bool) (DeploySpec, error) {
	return ResolvePublishedDevopsDeploySpecWithReason(ctx, target, versionOverride, "no local runtime chart", runtimeChartOverride, runtimeImageExplicit)
}

func ResolvePublishedDevopsDeploySpecWithReason(ctx core.Context, target config.OpenResult, versionOverride, reason, runtimeChartOverride string, runtimeImageExplicit bool) (DeploySpec, error) {
	// Every chart probe below (the runtime ladder and the tenant-chart check)
	// reads a registry. A deploy running inside the target env's own runtime pod
	// resolves the credential that env declared for itself first, so a private
	// namespace read is definitive instead of anonymous-and-therefore-refused.
	registrypkg.ConfigureInPodDeclaredRegistryAuth(ctx, target)
	registry := publishedDevopsChartRegistry(target)
	version := strings.TrimSpace(versionOverride)
	if version == "" {
		version = strings.TrimSpace(target.EnvConfig.RuntimeVersion)
	}
	if version == "" {
		// Dry-run contract: the decision that stops the plan must surface as a
		// trace line before the error return.
		ctx.Trace("deploy: " + reason + " and no runtime version resolved; cannot deploy the published " + config.DevopsComponentName + " chart")
		return DeploySpec{}, fmt.Errorf("runtime version is required to deploy the published %s chart: pass --version or persist runtimeversion in the env config", config.DevopsComponentName)
	}

	chart, err := resolveRuntimeChartCoordinate(ctx, target, registry, version, reason, strings.TrimSpace(runtimeChartOverride) != "")
	if err != nil {
		return DeploySpec{}, err
	}
	traceRuntimeRegistryMemo(ctx, target, registry, chart.registry)

	deployContext := shared.KubernetesDeployContext{
		ComponentName: config.DevopsComponentName,
		ChartPath:     chart.reference,
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
	deployInput.SubchartKey = publishedUmbrellaSubchartKey(target.Tenant, chart.name)
	deployInput.ChartVersion = chart.version
	deployInput.ChartCandidates = chart.candidates
	if chart.movedPin {
		// The env stated the older version, so the config still records it:
		// without this the next deploy reads the same lagging pin back and the
		// roll never converges.
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
		// No override applies, so the chart's own stock default wins -- see
		// service.yaml's devopsImage default, `<registry>/erun-devops:<appVersion>`.
		// That is fully known here even though nothing is threaded to helm for
		// it, so record it rather than leaving the memo empty.
		deployInput.ResolvedRuntimeImage = registry + "/" + config.DefaultRuntimeImageName + ":" + deployInput.resolvedChartVersion()
	}
	deployInput.PersistRuntimeImage = persistImage
	explicitLineChange := runtimeImageExplicit || strings.TrimSpace(runtimeChartOverride) != ""
	if err := guardRuntimeCoordinate(ctx, target, chart, runtimeChartOverride, deployInput, explicitLineChange); err != nil {
		return DeploySpec{}, err
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
func resolvePublishedComponentDeploySpec(ctx core.Context, target config.OpenResult, componentName, versionOverride string) (DeploySpec, error) {
	registry := publishedTenantComponentChartRegistry(target)
	version := strings.TrimSpace(versionOverride)
	if version == "" {
		version = strings.TrimSpace(target.EnvConfig.RuntimeVersion)
	}
	if version == "" {
		// Dry-run contract: trace the stopping decision before the error return.
		ctx.Trace("deploy: no version resolved for component " + componentName + "; cannot deploy its published chart")
		return DeploySpec{}, fmt.Errorf("version is required to deploy the published %s chart: pass --version or persist runtimeversion in the env config", componentName)
	}

	chartReference := helm.PublishedDevopsChartOCIRepo(registry) + "/" + componentName
	ctx.Trace("deploy: no local chart for " + componentName + "; using published chart " + chartReference + " version " + version)

	deployContext := shared.KubernetesDeployContext{
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

// publishedDevopsChartRegistry prefers the env's recorded RuntimeRegistry so a
// reopen keeps addressing the registry the env was deployed from.
func publishedDevopsChartRegistry(target config.OpenResult) string {
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

// resolveRuntimeRegistry is the registry projected into the runtime pod as
// RUNTIME_REGISTRY (nested in-pod image resolution). Prefer the persisted
// runtimeregistry; when it is empty a `--cluster-registry` env falls back to the
// runtime image's own registry — the same precedence publishedDevopsChartRegistry
// uses — so the pod resolves nested platform images from where they are published
// (e.g. ghcr) rather than the in-cluster registry that never held them. A plain
// env projects nothing here: its runtime registry follows the deploy registry the
// chart already renders, never a runtime-image override.
func resolveRuntimeRegistry(target config.OpenResult) string {
	if r := strings.TrimSpace(target.EnvConfig.RuntimeRegistry); r != "" {
		return r
	}
	if isClusterRegistryEnv(target) {
		return config.RuntimeImageRegistry(target.EnvConfig.RuntimeImage)
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
func isClusterRegistryEnv(target config.OpenResult) bool {
	return target.EnvConfig.ContainerRegistries.HasClusterEntry() ||
		strings.TrimSpace(target.ClusterPullRegistry) != ""
}

// RuntimeChartConfirmationError reports that a by-reference deploy's runtime
// chart search ended with no candidate confirmed published at the requested
// version -- never a reason to substitute an unconfirmed coordinate, because
// the shared erun-devops chart is versioned on erun's own release line and a
// tenant's own version can never be a valid coordinate for it. Candidates
// names each coordinate the ladder probed and its answer: "confirmed absent"
// for a registry read that succeeded and excluded the version, or "could not
// determine: <err>" for a read that failed outright (auth, network, an
// unreachable registry) -- an answer that must never be read as absence.
type RuntimeChartConfirmationError struct {
	Version    string
	Candidates []string
	// Inconclusive is true when at least one candidate's registry read failed
	// outright, so the search cannot say the chart is genuinely unpublished --
	// only that it could not confirm one.
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
