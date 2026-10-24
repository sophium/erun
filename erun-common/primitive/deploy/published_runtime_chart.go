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
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// ociChartReferenceRegistry is PublishedDevopsChartOCIRepo's inverse: the
// registry a published-chart reference addresses, or "" for a reference that is
// not an OCI chart URL under that path.
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
	return helm.PublishedDevopsChartOCIRepo(c.registry) + "/" + c.chart
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

// platformChartRegistry names the registry erun's own artifacts come from for
// this env: the runtime image's registry when the env states one, else erun's
// default. Both are the same claim — the platform chart is published beside the
// platform image, never beside the tenant's app images.
func platformChartRegistry(target config.OpenResult) (registry, why string) {
	if imageRegistry := config.RuntimeImageRegistry(target.EnvConfig.RuntimeImage); imageRegistry != "" {
		return imageRegistry, "the shared platform chart in the runtime image's registry"
	}
	return config.DefaultContainerRegistry, "the shared platform chart in erun's own registry"
}

// resolvedRuntimeChart is the coordinate a runtime deploy installs: the chart
// reference, its name, the version to pull it at (empty means the deploy
// version), and the registry the chart resolved from. registry is empty for a
// chart the env states outright, which is a coordinate the operator gave rather
// than one a search produced.
type resolvedRuntimeChart struct {
	reference  string
	name       string
	version    string
	registry   string
	candidates []string
	// searched is true when the candidate ladder produced this coordinate, and
	// false when it is the coordinate the env states outright. The distinction
	// is what makes the coordinate the operator's own: a chart the env states is
	// the operator saying so, in config, and erun installs it as given (see
	// resolveRuntimeChartCoordinate) -- including a tenant that deliberately
	// states the stock erun-devops chart on erun's line while running its own
	// image line, which is a legitimate configuration and not an inference
	// erun made. Only a searched coordinate is erun's own conclusion, and so
	// only a searched one is checked against the line the environment runs.
	searched bool
	// movedPin is true when the env's own stated chart version was a lagging
	// pin on the deploy's line and version -- rather than the coordinate the
	// env states -- so the caller records the chart it actually installs back
	// to EnvConfig.RuntimeChart. Left false for a chart stated at a version the
	// deploy is not on, and for one with no version of its own, which already
	// follows the deploy version.
	movedPin bool
}

// resolvePublishedRuntimeChartReference walks the candidate ladder and installs
// the first coordinate confirmed to publish the deploy version, probed against
// the chart repo (authenticated like every registry read). Every candidate
// tried and passed over is traced, so a dry-run reader sees the whole search
// rather than only its answer. It never substitutes an unconfirmed coordinate:
// when no candidate is confirmed published, it refuses rather than guessing --
// the shared erun-devops chart is versioned on erun's own release line, so
// installing it at a tenant's own version is a coordinate that can never
// exist. A registry read that fails outright (auth, network, an unreachable
// registry) is reported as "could not determine", never folded into "not
// found": a blind probe is not evidence of absence.
//
// deferToOverride is true when the caller already knows this search's answer
// is about to be replaced by an operator-stated --runtime-chart: refusing here
// would block a deploy the operator has already resolved themselves, so the
// search reports what it found (or didn't) without failing the deploy, and the
// caller installs a placeholder coordinate that the override immediately
// supersedes.
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

// resolveRuntimeChartCoordinate answers which runtime chart to install, at which
// version. An env that states its chart (EnvConfig.RuntimeChart) is taken at its
// word -- that is the coordinate, and its version, when it carries one, is the
// chart's own rather than the deploy version. Otherwise the chart is looked up
// along the candidate ladder, at the deploy version. deferToOverride is
// forwarded to resolvePublishedRuntimeChartReference -- see its doc comment.
//
// The returned chart version is empty for the looked-up case, meaning "the deploy
// version", so nothing changes for the envs whose chart and image were published
// as a pair.
//
// A stated version is normally taken as the chart's own, which is how an env
// rides a chart on another line entirely. The exceptions are both cases where
// the chart and the image the same deploy installs are one coordinate that must
// move together: a stated stock erun-devops chart on the deploy's own line at a
// version the deploy has moved past (stockRuntimePinMovesWithDeployVersion),
// and a stated <tenant>-devops umbrella at a version behind the deploy on that
// umbrella's own line (resolveTenantUmbrellaPin). Honoring either installs the
// older chart while the deploy records the newer version, so the operator reads
// a version roll that did not happen.
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

// resolveTenantUmbrellaPin decides what a stated <tenant>-devops umbrella does
// when the deploy is at a different version on that umbrella's own line, and
// reports the decision. It returns handled=false when the stated chart is not
// the tenant's own umbrella, leaving the coordinate to the caller's own trace.
//
// A tenant that publishes its own artifacts runs its runtime on the tenant's
// own version line: <tenant>-devops wraps the canonical erun-devops as a
// subchart, and it is the umbrella that names the erun version the environment
// actually runs. --version names that same line, so a stated umbrella behind it
// is a lagging pin, not a cross-line coordinate: the image the same deploy
// derives is the deploy version, and honoring the older umbrella installs a
// newer runtime image under an umbrella wrapped around an older erun -- then
// reports success. This is the tenant-umbrella counterpart of
// stockRuntimePinMovesWithDeployVersion, which covers the stock chart on erun's
// own line; a tenant stating the *stock* erun-devops chart is genuinely naming
// another line (that tenant's own line is not erun's) and stays untouched.
//
// The move is never a guess. The deploy version must be confirmed published for
// this chart -- the same probe the by-reference ladder uses -- because a
// tenant's umbrella is published per version by the tenant's own release, so a
// coordinate without a published chart behind it cannot be installed. An
// unconfirmed deploy version (absent, or a registry read that failed outright
// and is therefore not evidence of absence) leaves the pin alone and says so
// rather than staying silent: the reported failure's whole harm was that
// nothing distinguished a moved umbrella from a stranded one.
//
// overrideComing is true when an operator-stated --runtime-chart is about to
// replace this coordinate wholesale; the hold is reported against the
// coordinate that override discards, so nothing is said or probed here.
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

// laggingTenantUmbrellaPin answers whether a stated chart coordinate is the
// tenant's own runtime umbrella left at a version this deploy has moved past,
// and names the chart, the version it states, and the version to move it to.
func laggingTenantUmbrellaPin(target config.OpenResult, chart resolvedRuntimeChart, deployVersion string, overrideComing bool) (name, stated, version string, ok bool) {
	if overrideComing || !isTenantRuntimeUmbrella(target.Tenant, chart.name) {
		return "", "", "", false
	}
	stated, version = strings.TrimSpace(chart.version), strings.TrimSpace(deployVersion)
	if stated == "" || version == "" || stated == version {
		// Nothing to decide: either the chart already rides the deploy version,
		// or there is no version to move it to.
		return "", "", "", false
	}
	return strings.TrimSpace(chart.name), stated, version, true
}

// isTenantRuntimeUmbrella reports whether chartName is the runtime umbrella a
// tenant publishes for itself, rather than the shared erun-devops chart the
// canonical product tenant installs directly.
func isTenantRuntimeUmbrella(tenant, chartName string) bool {
	tenant, chartName = strings.TrimSpace(tenant), strings.TrimSpace(chartName)
	if tenant == "" || chartName == "" {
		return false
	}
	return chartName == config.RuntimeReleaseName(tenant) && chartName != config.DevopsComponentName
}

// tenantUmbrellaHoldTrace is the one message both unconfirmed outcomes share:
// what the deploy left in place, why it could not move it, and the remedy.
// reason is the outcome-specific clause.
func tenantUmbrellaHoldTrace(chart resolvedRuntimeChart, name, stated, version, reason string) string {
	return "deploy: holding back the env's runtime umbrella " + chart.reference + " at " + stated + ": the tenant's own " + name + " " +
		reason + ". The runtime image still moves to " + version + ", so the umbrella stays wrapped around an older " + config.DevopsComponentName +
		" than the one deployed; publish the tenant's charts at " + version + " (`erun push --version " + version + "`) and redeploy"
}

// tenantUmbrellaChartRegistry answers which registry an umbrella reference is
// probed in: the one the reference itself addresses, so the line under question
// is the one the operator pointed at, falling back to the deploy registry the
// tenant's own charts are published to for a reference that is not an OCI chart
// URL.
func tenantUmbrellaChartRegistry(target config.OpenResult, reference string) string {
	if registry := ociChartReferenceRegistry(reference); registry != "" {
		return registry
	}
	return publishedTenantComponentChartRegistry(target)
}

// traceRuntimeRegistryMemo surfaces what this deploy does to the env's
// runtimeregistry memo when the chart resolved somewhere other than where the
// search started — the only case where the memo changes meaning. An env with no
// memo gets the registry the chart actually came from, so the next search
// short-circuits there; an env that already names one keeps it, because the memo
// is how an operator redirects this search and a deploy must not take that choice
// back silently.
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
	base, ok := strings.CutPrefix(chartName, shared.TenantResourcePrefix(tenant)+"-")
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
func ensureTenantChartsPublished(ctx core.Context, target config.OpenResult, versionOverride string, runtimeSelected bool, components []string) error {
	version := strings.TrimSpace(versionOverride)
	if version == "" {
		version = strings.TrimSpace(target.EnvConfig.RuntimeVersion)
	}
	if version == "" {
		ctx.Trace("deploy: no version resolved; cannot verify the tenant's charts are published")
		return fmt.Errorf("version is required to deploy the tenant's charts: pass --version or persist runtimeversion in the env config")
	}
	// Components are published to the DEPLOY registry by `erun push`, never the
	// platform-chart registry publishedDevopsChartRegistry resolves (an explicit
	// runtimeregistry, or the runtime image's own registry for a
	// cluster-registry env) -- a tenant's own artifacts are never published
	// beside the platform image.
	registry := publishedTenantComponentChartRegistry(target)

	required := make([]string, 0, len(components)+1)
	runtimeChart := config.RuntimeReleaseName(target.Tenant)
	// An env that states its runtime chart is not riding a tenant chart at this
	// version, so requiring one here would fail a deploy that is perfectly
	// coherent: the components run on the tenant's line, the runtime chart on the
	// line the env named. Its components are still verified.
	runtimeChartStated := strings.TrimSpace(target.EnvConfig.RuntimeChart) != ""
	runtimeRegistry := registry
	switch {
	case runtimeSelected && runtimeChartStated:
		ctx.Trace("deploy: the env states its runtime chart " + strings.TrimSpace(target.EnvConfig.RuntimeChart) + "; verifying only the tenant's component charts at " + version)
	case runtimeSelected && runtimeChart != config.DevopsComponentName:
		// The tenant's own runtime chart is normally published alongside its
		// components by the same `erun push`, so it usually shares their
		// registry and is verified together, below. When it doesn't -- an
		// explicit runtimeregistry, or the cluster-registry env's platform-chart
		// fallback -- it is verified separately, against the same registry
		// resolvePublishedDevopsDeploySpecWithReason's own ladder actually
		// installs it from.
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

// publishedTenantComponentChartRegistry answers where a tenant's own
// component charts (and its own runtime chart, when it publishes one) are
// published: `erun push` writes them to the DEPLOY registry -- concretized to
// its in-cluster pull host for a cluster-registry env -- never the
// runtimeregistry override or the runtime-image fallback
// publishedDevopsChartRegistry applies for the shared platform chart. A
// tenant's own artifacts are never published beside the platform image, so
// following that registry here probed a registry the tenant's charts were
// never going to be in.
func publishedTenantComponentChartRegistry(target config.OpenResult) string {
	if registry, ok := target.EnvConfig.ContainerRegistries.DeployRegistry(); ok {
		return registry
	}
	if registry := resolveProjectContainerRegistry(target.RepoPath, target.Environment); registry != "" {
		return registry
	}
	return config.DefaultContainerRegistry
}

// chartRegistryInsecure reports whether registry is the deploy target's own
// concretized cluster registry, marked insecure (plain HTTP) on its cluster:
// entry. It is the only registry a chart probe can ever need to address over
// plain HTTP -- erun's own platform registries (ghcr.io, an ECR account, a
// project's static `registry:` entry) are always TLS.
func chartRegistryInsecure(target config.OpenResult, registry string) bool {
	registry = strings.TrimSpace(registry)
	return registry != "" && target.ClusterRegistryInsecure && registry == strings.TrimSpace(target.ClusterPullRegistry)
}

// reportUnconfirmedTenantCharts probes each chart in required and returns an
// error naming whichever charts could not be confirmed published at version --
// distinguishing "could not determine" (a probe error, which must never be read
// as "not published") from "confirmed absent" (missing). A probe that could not
// answer takes priority: it is not evidence the chart is missing, so refusing on
// it takes precedence over refusing on a genuine miss.
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

// probeChartVersion answers whether chartName publishes version in
// containerRegistry. A non-nil error means the registry read itself failed --
// credentials, network, or an unreachable registry -- and must never be read as
// "not found": a blind probe is not evidence of absence, only a definitive read
// that excludes the version is.
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
