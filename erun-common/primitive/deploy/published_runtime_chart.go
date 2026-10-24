package deploy

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/helm"
	registrypkg "github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/shared/opentarget"
)

// ociChartReferenceRegistry is PublishedDevopsChartOCIRepo's inverse, or "" for a non-OCI reference.
func ociChartReferenceRegistry(reference string) string {
	trimmed := strings.TrimSpace(reference)
	if !isOCIChartReference(trimmed) {
		return ""
	}
	registry, _, ok := strings.Cut(strings.TrimPrefix(trimmed, "oci://"), "/"+helm.PublishedChartRepoPath+"/")
	if !ok {
		return ""
	}
	return registry
}

// runtimeChartCandidate carries why its registry is a place to look, for traces and errors.
type runtimeChartCandidate struct {
	registry string
	chart    string
	why      string
}

func (c runtimeChartCandidate) reference() string {
	return helm.PublishedDevopsChartOCIRepo(c.registry) + "/" + c.chart
}

func (c runtimeChartCandidate) describe() string {
	return strings.TrimSpace(c.registry) + "/charts/" + c.chart + " (" + c.why + ")"
}

// runtimeChartCandidates orders the places a runtime chart can be: the tenant
// umbrella first, then the shared chart in the deploy registry, then beside the
// runtime image, since erun publishes erun-devops only where it releases.
func runtimeChartCandidates(target config.OpenResult, chartRegistry string) []runtimeChartCandidate {
	chartRegistry = strings.TrimSpace(chartRegistry)
	candidates := make([]runtimeChartCandidate, 0, 3)
	if tenantChart := config.RuntimeReleaseName(target.Tenant); tenantChart != config.DevopsComponentName {
		candidates = append(candidates, runtimeChartCandidate{chartRegistry, tenantChart, "the tenant's own umbrella"})
	}
	candidates = append(candidates, runtimeChartCandidate{chartRegistry, config.DevopsComponentName, "the shared platform chart"})
	platformRegistry, why := platformChartRegistry(target)
	if platformRegistry != "" && platformRegistry != chartRegistry {
		candidates = append(candidates, runtimeChartCandidate{platformRegistry, config.DevopsComponentName, why})
	}
	return candidates
}

// platformChartRegistry is where erun's platform chart is published: beside the platform image.
func platformChartRegistry(target config.OpenResult) (registry, why string) {
	if imageRegistry := config.RuntimeImageRegistry(target.EnvConfig.RuntimeImage); imageRegistry != "" {
		return imageRegistry, "the shared platform chart in the runtime image's registry"
	}
	return config.DefaultContainerRegistry, "the shared platform chart in erun's own registry"
}

// resolvedRuntimeChart is the coordinate a runtime deploy installs; an empty
// version means the deploy version.
type resolvedRuntimeChart struct {
	reference  string
	name       string
	version    string
	registry   string
	candidates []string
	// searched marks erun's own conclusion; only a searched coordinate is checked
	// against the env's line, while a stated one is installed as given.
	searched bool
	// movedPin is true when a lagging stated pin was moved to the deploy version and
	// must be recorded back to EnvConfig.RuntimeChart.
	movedPin bool
}

// resolvePublishedRuntimeChartReference installs the first candidate confirmed to
// publish the version and never substitutes an unconfirmed one; a failed read is
// "could not determine", not "not found". With deferToOverride it reports without
// failing, since --runtime-chart will replace the coordinate.
func resolvePublishedRuntimeChartReference(ctx core.Context, target config.OpenResult, chartRegistry, version string, deferToOverride bool) (resolvedRuntimeChart, error) {
	probed := runtimeChartCandidates(target, chartRegistry)
	candidates := make([]string, 0, len(probed))
	for _, candidate := range probed {
		candidates = append(candidates, candidate.describe())
	}
	outcomes := make([]string, 0, len(probed))
	inconclusive := false
	for _, candidate := range probed {
		found, err := probeChartVersion(context.Background(), candidate.registry, candidate.chart, version, chartRegistryInsecure(target, candidate.registry))
		if err != nil {
			ctx.Trace("deploy: runtime chart " + candidate.chart + " " + version + " could not be confirmed in " + candidate.registry + " (" + candidate.why + "): " + err.Error())
			outcomes = append(outcomes, candidate.describe()+": could not determine: "+err.Error())
			inconclusive = true
			continue
		}
		if found {
			ctx.Trace("deploy: runtime chart " + candidate.chart + " " + version + " found in " + candidate.registry + " (" + candidate.why + ")")
			return resolvedRuntimeChart{reference: candidate.reference(), name: candidate.chart, registry: candidate.registry, candidates: candidates, searched: true}, nil
		}
		ctx.Trace("deploy: runtime chart " + candidate.chart + " " + version + " not found in " + candidate.registry + " (" + candidate.why + ")")
		outcomes = append(outcomes, candidate.describe()+": confirmed absent")
	}
	if deferToOverride {
		fallback := runtimeChartCandidate{strings.TrimSpace(chartRegistry), config.DevopsComponentName, "the shared platform chart"}
		ctx.Trace("deploy: no runtime chart candidate confirmed at " + version + "; --runtime-chart names the coordinate to install instead")
		return resolvedRuntimeChart{reference: fallback.reference(), name: fallback.chart, registry: fallback.registry, candidates: candidates, searched: true}, nil
	}
	ctx.Trace("deploy: no runtime chart candidate confirmed at " + version + "; refusing to guess")
	return resolvedRuntimeChart{}, &RuntimeChartConfirmationError{Version: version, Candidates: outcomes, Inconclusive: inconclusive}
}

// resolveRuntimeChartCoordinate takes an env's stated chart at its word, else
// searches the ladder at the deploy version (returned version empty). A stated
// version moves only when chart and image are one coordinate: a lagging stock
// pin on the deploy's line, or a lagging tenant umbrella.
func resolveRuntimeChartCoordinate(ctx core.Context, target config.OpenResult, registry, version, reason string, deferToOverride bool) (resolvedRuntimeChart, error) {
	if named := strings.TrimSpace(target.EnvConfig.RuntimeChart); named != "" {
		reference, chartVersion := SplitChartReferenceVersion(named)
		chart := resolvedRuntimeChart{reference: reference, name: ChartName(reference), version: chartVersion}
		if chartVersion == "" {
			ctx.Trace("deploy: " + reason + "; using the env's runtime chart " + reference + " at the deploy version " + version)
			return chart, nil
		}
		if stockRuntimePinMovesWithDeployVersion(target.Tenant, target.EnvConfig, chart.name, chartVersion, version) {
			ctx.Trace("deploy: the env's runtime chart " + reference + " is pinned at " + chartVersion +
				", which is behind this deploy's " + version + " on " + config.DevopsComponentName + "'s own release line; moving the pin to the deploy version")
			chart.version = strings.TrimSpace(version)
			chart.movedPin = true
			return chart, nil
		}
		if settled, handled := resolveTenantUmbrellaPin(ctx, target, chart, version, deferToOverride); handled {
			return settled, nil
		}
		ctx.Trace("deploy: " + reason + "; using the env's runtime chart " + reference + " version " + chartVersion)
		return chart, nil
	}
	chart, err := resolvePublishedRuntimeChartReference(ctx, target, registry, version, deferToOverride)
	if err != nil {
		return resolvedRuntimeChart{}, err
	}
	ctx.Trace("deploy: " + reason + "; using published chart " + chart.reference + " version " + version)
	return chart, nil
}

// resolveTenantUmbrellaPin moves a lagging <tenant>-devops umbrella to the deploy
// version only when that version is confirmed published, and otherwise reports
// the hold. handled=false means the chart is not the tenant's umbrella. With
// overrideComing nothing is probed or reported.
func resolveTenantUmbrellaPin(ctx core.Context, target config.OpenResult, chart resolvedRuntimeChart, deployVersion string, overrideComing bool) (resolvedRuntimeChart, bool) {
	name, stated, version, lagging := laggingTenantUmbrellaPin(target, chart, deployVersion, overrideComing)
	if !lagging {
		return chart, false
	}
	registry := tenantUmbrellaChartRegistry(target, chart.reference)
	found, err := probeChartVersion(context.Background(), registry, name, version, chartRegistryInsecure(target, registry))
	switch {
	case err != nil:
		ctx.Trace(tenantUmbrellaHoldTrace(chart, name, stated, version,
			"could not be confirmed at this deploy's "+version+" in "+registry+" ("+err.Error()+") — the deploy will not move a pin it cannot confirm"))
	case !found:
		ctx.Trace(tenantUmbrellaHoldTrace(chart, name, stated, version,
			"has no published chart at this deploy's "+version+" in "+registry+" — the deploy will not install a coordinate that cannot exist"))
	default:
		ctx.Trace("deploy: the env's runtime umbrella " + chart.reference + " is pinned at " + stated +
			", which is behind this deploy's " + version + " on " + name + "'s own release line; moving the pin to the deploy version")
		chart.version = version
		chart.movedPin = true
	}
	return chart, true
}

func laggingTenantUmbrellaPin(target config.OpenResult, chart resolvedRuntimeChart, deployVersion string, overrideComing bool) (name, stated, version string, ok bool) {
	if overrideComing || !isTenantRuntimeUmbrella(target.Tenant, chart.name) {
		return "", "", "", false
	}
	stated, version = strings.TrimSpace(chart.version), strings.TrimSpace(deployVersion)
	if stated == "" || version == "" || stated == version {
		return "", "", "", false
	}
	return strings.TrimSpace(chart.name), stated, version, true
}

func isTenantRuntimeUmbrella(tenant, chartName string) bool {
	tenant, chartName = strings.TrimSpace(tenant), strings.TrimSpace(chartName)
	if tenant == "" || chartName == "" {
		return false
	}
	return chartName == config.RuntimeReleaseName(tenant) && chartName != config.DevopsComponentName
}

func tenantUmbrellaHoldTrace(chart resolvedRuntimeChart, name, stated, version, reason string) string {
	return "deploy: holding back the env's runtime umbrella " + chart.reference + " at " + stated + ": the tenant's own " + name + " " +
		reason + ". The runtime image still moves to " + version + ", so the umbrella stays wrapped around an older " + config.DevopsComponentName +
		" than the one deployed; publish the tenant's charts at " + version + " (`erun push --version " + version + "`) and redeploy"
}

// tenantUmbrellaChartRegistry probes the registry the reference addresses, else the deploy registry.
func tenantUmbrellaChartRegistry(target config.OpenResult, reference string) string {
	if registry := ociChartReferenceRegistry(reference); registry != "" {
		return registry
	}
	return publishedTenantComponentChartRegistry(target)
}

// traceRuntimeRegistryMemo: an env with no memo records where the chart came from;
// an existing memo is the operator's redirect and is kept.
func traceRuntimeRegistryMemo(ctx core.Context, target config.OpenResult, searchedFrom, resolvedFrom string) {
	resolvedFrom = strings.TrimSpace(resolvedFrom)
	if resolvedFrom == "" || resolvedFrom == strings.TrimSpace(searchedFrom) {
		return
	}
	if recorded := strings.TrimSpace(target.EnvConfig.RuntimeRegistry); recorded != "" {
		ctx.Trace("deploy: the env's runtime registry " + recorded + " stands; the runtime chart resolved from " + resolvedFrom + " instead (`erun init " + target.Tenant + " " + target.Environment + " --runtime-registry " + resolvedFrom + "` changes it)")
		return
	}
	ctx.Trace("deploy: recording runtime registry " + resolvedFrom + ", where the runtime chart resolved, rather than " + strings.TrimSpace(searchedFrom) + ", where the search started")
}

// publishedUmbrellaSubchartKey returns the erun-<base> subchart scope a tenant
// umbrella wraps, or "" for a canonical chart; helm does not pass top-level --sets
// into subcharts.
func publishedUmbrellaSubchartKey(tenant, chartName string) string {
	chartName = strings.TrimSpace(chartName)
	base, ok := strings.CutPrefix(chartName, opentarget.TenantResourcePrefix(tenant)+"-")
	if !ok {
		return ""
	}
	canonical := canonicalChartPrefix + "-" + base
	if chartName == canonical {
		return ""
	}
	return canonical
}

// ensureTenantChartsPublished verifies up front that the tenant runtime and
// component charts exist at the version, so the deploy neither falls back to
// erun-devops nor half-applies.
func ensureTenantChartsPublished(ctx core.Context, target config.OpenResult, versionOverride string, runtimeSelected bool, components []string) error {
	version := strings.TrimSpace(versionOverride)
	if version == "" {
		version = strings.TrimSpace(target.EnvConfig.RuntimeVersion)
	}
	if version == "" {
		ctx.Trace("deploy: no version resolved; cannot verify the tenant's charts are published")
		return fmt.Errorf("version is required to deploy the tenant's charts: pass --version or persist runtimeversion in the env config")
	}
	// `erun push` publishes tenant charts to the deploy registry, never beside the platform image.
	registry := publishedTenantComponentChartRegistry(target)

	required := make([]string, 0, len(components)+1)
	runtimeChart := config.RuntimeReleaseName(target.Tenant)
	// An env stating its runtime chart is coherent without a tenant runtime chart; its components are still verified.
	runtimeChartStated := strings.TrimSpace(target.EnvConfig.RuntimeChart) != ""
	runtimeRegistry := registry
	switch {
	case runtimeSelected && runtimeChartStated:
		ctx.Trace("deploy: the env states its runtime chart " + strings.TrimSpace(target.EnvConfig.RuntimeChart) + "; verifying only the tenant's component charts at " + version)
	case runtimeSelected && runtimeChart != config.DevopsComponentName:
		runtimeRegistry = publishedDevopsChartRegistry(target)
		if runtimeRegistry == registry {
			required = append(required, runtimeChart)
		}
	}
	required = append(required, components...)

	ctx.Trace("deploy: deploying tenant artifacts; verifying charts published at " + version + " in " + registry + ": " + strings.Join(required, ", "))
	if err := reportUnconfirmedTenantCharts(required, registry, chartRegistryInsecure(target, registry), version); err != nil {
		return err
	}

	if runtimeRegistry != registry {
		ctx.Trace("deploy: deploying tenant artifacts; verifying the runtime chart " + runtimeChart + " published at " + version + " in " + runtimeRegistry)
		return reportUnconfirmedTenantCharts([]string{runtimeChart}, runtimeRegistry, chartRegistryInsecure(target, runtimeRegistry), version)
	}
	return nil
}

// publishedTenantComponentChartRegistry is the deploy registry `erun push` writes
// to, concretized for a cluster-registry env.
func publishedTenantComponentChartRegistry(target config.OpenResult) string {
	if registry, ok := target.EnvConfig.ContainerRegistries.DeployRegistry(); ok {
		return registry
	}
	if registry := resolveProjectContainerRegistry(target.RepoPath, target.Environment); registry != "" {
		return registry
	}
	return config.DefaultContainerRegistry
}

// chartRegistryInsecure: only the target's concretized cluster registry may be plain HTTP.
func chartRegistryInsecure(target config.OpenResult, registry string) bool {
	registry = strings.TrimSpace(registry)
	return registry != "" && target.ClusterRegistryInsecure && registry == strings.TrimSpace(target.ClusterPullRegistry)
}

// reportUnconfirmedTenantCharts refuses on "could not determine" ahead of
// "confirmed absent", since a failed probe is not evidence of absence.
func reportUnconfirmedTenantCharts(required []string, registry string, insecure bool, version string) error {
	missing := make([]string, 0, len(required))
	unresolved := make([]string, 0, len(required))
	for _, chart := range required {
		found, err := probeChartVersion(context.Background(), registry, chart, version, insecure)
		switch {
		case err != nil:
			unresolved = append(unresolved, chart+": could not determine: "+err.Error())
		case !found:
			missing = append(missing, chart)
		}
	}
	if len(unresolved) > 0 {
		return fmt.Errorf("deploy could not confirm whether these tenant charts are published at version %s in %s: %s; deploy refuses to guess rather than treat an unanswered probe as published -- check registry access and retry", version, registry, strings.Join(unresolved, "; "))
	}
	if len(missing) > 0 {
		return fmt.Errorf("deploy rolls out the tenant's own artifacts, which run on the tenant's version line, but these charts are not published at version %s in %s: %s; `erun push --version %s` (or `erun build --release`) publishes the tenant's runtime and component charts together, so publish the missing chart(s) then deploy", version, registry, strings.Join(missing, ", "), version)
	}
	return nil
}

// probeChartVersion returns an error only when the read failed, which must never be read as "not found".
func probeChartVersion(ctx context.Context, containerRegistry, chartName, version string, insecure bool) (bool, error) {
	if override, ok := os.LookupEnv(PublishedChartProbeOverrideEnv); ok {
		return publishedChartOverrideHasVersion(override, containerRegistry, chartName, version), nil
	}
	versions, err := registrypkg.ResolveConfiguredRuntimeRegistryVersions(ctx, config.RuntimeRegistryConfig{
		Namespace:  containerRegistry,
		Repository: helm.PublishedChartRepoPath + "/" + chartName,
		Insecure:   insecure,
	})
	if err != nil {
		return false, err
	}
	return versions.HasVersion(version), nil
}
