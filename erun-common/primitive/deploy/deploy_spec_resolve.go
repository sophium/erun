package deploy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
	registrypkg "github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func ResolveDeploySpec(ctx core.Context, store DeployStore, findProjectRoot shared.ProjectFinderFunc, resolveDockerBuildContext shared.BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now shared.NowFunc, target DeployTarget, componentName, versionOverride string) (DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, _, now = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	versionOverride = resolveDeployVersionOverride(target, versionOverride)

	resolvedTarget, err := resolveDeployTarget(ctx.Command, ctx.CommandScopesTenantByFlag, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
	if err != nil {
		return DeploySpec{}, err
	}
	spec, err := resolveDeploySpecForOpenResult(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, resolvedTarget, componentName, versionOverride, nil)
	if err != nil {
		return DeploySpec{}, err
	}
	override, ok, err := resolveRolloutTimeoutOverride(target.RolloutTimeout)
	if err != nil {
		return DeploySpec{}, err
	}
	if ok {
		spec.Deploy.Timeout = override
	}
	if !target.NamespaceQuotaOverride.IsZero() {
		if err := configpkg.ValidateNamespaceResourceQuota(target.NamespaceQuotaOverride); err != nil {
			return DeploySpec{}, fmt.Errorf("namespace quota override: %w", err)
		}
		spec.Deploy.NamespaceQuota = target.NamespaceQuotaOverride
	}
	specs := []DeploySpec{spec}
	if err := finalizeRuntimeChartSpecs(ctx, target, resolvedTarget, specs); err != nil {
		return DeploySpec{}, err
	}
	return specs[0], nil
}

// ResolveCurrentDeploySpecs resolves specs for `erun deploy` — the pure deploy
// primitive that installs a version by reference and never builds.
func ResolveCurrentDeploySpecs(ctx core.Context, store DeployStore, findProjectRoot shared.ProjectFinderFunc, resolveDockerBuildContext shared.BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now shared.NowFunc, target DeployTarget) ([]DeploySpec, error) {
	specs, err := ResolveCurrentDeploySpecsOrchestrated(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, false)
	if err != nil {
		return nil, err
	}
	override, ok, err := resolveRolloutTimeoutOverride(target.RolloutTimeout)
	if err != nil {
		return nil, err
	}
	if ok {
		for i := range specs {
			specs[i].Deploy.Timeout = override
		}
	}
	if !target.NamespaceQuotaOverride.IsZero() {
		if err := configpkg.ValidateNamespaceResourceQuota(target.NamespaceQuotaOverride); err != nil {
			return nil, fmt.Errorf("namespace quota override: %w", err)
		}
		for i := range specs {
			specs[i].Deploy.NamespaceQuota = target.NamespaceQuotaOverride
		}
	}
	return specs, nil
}

// resolveRolloutTimeoutOverride validates an explicit per-deploy rollout-timeout
// override. A malformed or non-positive duration is a hard error so the deploy
// fails loudly rather than silently ignore the operator's input. Precedence:
// this override > env deploy.timeout > DefaultHelmDeploymentTimeout.
func resolveRolloutTimeoutOverride(raw string) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		return "", false, fmt.Errorf("invalid rollout timeout %q", raw)
	}
	if duration <= 0 {
		return "", false, fmt.Errorf("rollout timeout must be greater than zero")
	}
	return duration.String(), true, nil
}

// ResolveCurrentDeploySpecsOrchestrated is shared by `erun deploy` (buildOrchestration
// false → pure, version required) and the `build --deploy` orchestration
// (buildOrchestration true → reference the just-built working-tree image of a
// builds-here env). The build/push of that image is run by the build phase
// before these specs deploy; here it only supplies the image reference.
func ResolveCurrentDeploySpecsOrchestrated(ctx core.Context, store DeployStore, findProjectRoot shared.ProjectFinderFunc, resolveDockerBuildContext shared.BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now shared.NowFunc, target DeployTarget, buildOrchestration bool) ([]DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, _, now = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	now = freezeNow(now)

	resolvedTarget, err := resolveDeployTarget(ctx.Command, ctx.CommandScopesTenantByFlag, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
	if err != nil {
		return nil, err
	}

	runtimeImageOverride := strings.TrimSpace(target.RuntimeImageOverride)
	if runtimeImageOverride != "" {
		// The operator chose the runtime image explicitly. Record it on the env
		// so a later open/redeploy addresses the same image, and so the override
		// wins even when a repo-local runtime chart exists.
		resolvedTarget.EnvConfig.RuntimeImage = runtimeImageOverride
	}

	specs, err := resolveDeploySpecsForResolvedTarget(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, resolvedTarget, target, buildOrchestration, runtimeImageOverride)
	if err != nil {
		return nil, err
	}
	if err := finalizeRuntimeChartSpecs(ctx, target, resolvedTarget, specs); err != nil {
		return nil, err
	}
	return specs, nil
}

// finalizeRuntimeChartSpecs applies every guard and override that only fires
// when a resolved spec targets the runtime chart — the operator's chart
// override, the env's MCP-auth state, the MCP-auth downgrade refusal, and the
// in-pod local-agent redeploy refusal — in place, over whatever specs a path
// resolved. It is the single seam both `ResolveDeploySpec` (the
// component-named path) and `resolveCurrentDeploySpecs` (the current-deploy
// set) funnel through after resolving specs and before returning them, so a
// future path that resolves a spec able to name the runtime chart inherits
// these guards for free instead of needing to remember them.
func finalizeRuntimeChartSpecs(ctx core.Context, target DeployTarget, resolvedTarget configpkg.OpenResult, specs []DeploySpec) error {
	for i := range specs {
		applyRuntimeChartOverride(ctx, target, &specs[i])
		if err := ApplyMCPAuthToRuntimeSpec(ctx, target, &specs[i]); err != nil {
			return err
		}
		if err := guardMCPAuthDowngrade(ctx, target, specs[i].Deploy); err != nil {
			return err
		}
	}
	return guardInPodRuntimeDeploy(os.Getenv, resolvedTarget, specs)
}

func resolveDeploySpecsForResolvedTarget(ctx core.Context, store DeployStore, findProjectRoot shared.ProjectFinderFunc, resolveDockerBuildContext shared.BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now shared.NowFunc, resolvedTarget configpkg.OpenResult, target DeployTarget, buildOrchestration bool, runtimeImageOverride string) ([]DeploySpec, error) {
	// Resolve the credential this env declared for its own runtime pod before any
	// chart probe runs: the tenant-chart check below reads a registry ahead of the
	// runtime-chart ladder, and both must authenticate rather than probe
	// anonymously and be refused. Idempotent, so the ladder's own call is fine.
	registrypkg.ConfigureInPodDeclaredRegistryAuth(ctx, resolvedTarget)
	if resolvedTarget.RemoteRepo() {
		return resolvePublishedDeploySpecs(ctx, store, findProjectRoot, resolvedTarget, target)
	}
	return resolveSelectedLocalDeploySpecs(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, resolvedTarget, target, buildOrchestration, runtimeImageOverride)
}

// resolveSelectedLocalDeploySpecs resolves the opt-in-only deploy set for a
// local-repo target. It deploys exactly the selected charts — precedence:
// --components > the env's saved deploy.components > the repo k8s.deployments
// plan; an empty selection defaults to the runtime chart alone (bootstrap/heal).
// When the runtime is selected but the tenant vendors no repo-local runtime
// chart, the published erun-devops chart is appended so a plain deploy can
// bootstrap or heal the runtime.
func resolveSelectedLocalDeploySpecs(ctx core.Context, store DeployStore, findProjectRoot shared.ProjectFinderFunc, resolveDockerBuildContext shared.BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now shared.NowFunc, resolvedTarget configpkg.OpenResult, target DeployTarget, buildOrchestration bool, runtimeImageOverride string) ([]DeploySpec, error) {
	plan, err := loadProjectK8sPlanForRepo(resolvedTarget.RepoPath, resolvedTarget.Environment)
	if err != nil {
		return nil, err
	}
	selected, err := resolveGuardedDeploySelection(ctx, target, resolvedTarget, plan)
	if err != nil {
		return nil, err
	}
	traceConfiguredDeployPaths(ctx, resolvedTarget.RepoPath)

	deployContexts, err := resolveCurrentLocalDeployContexts(findProjectRoot, resolveKubernetesDeployContext, resolvedTarget, selected, plan)
	if err != nil {
		return nil, err
	}
	runtimeSelected := deploySelectionIncludesRuntime(selected, resolvedTarget.Tenant)
	hasLocalRuntime := containsRuntimeContext(deployContexts, resolvedTarget.Tenant)

	var currentBuild *shared.DockerBuildSpec
	if buildOrchestration && resolvedTarget.EnvConfig.BuildsHere() {
		// The build phase produces this version (a minted snapshot, or the
		// explicit --version); deploy references the built image by tag rather
		// than install-by-reference, which would verify a registry tag the
		// build has not pushed yet.
		currentBuild, err = resolveCurrentDockerComponentBuildForDeploy(ctx, store, findProjectRoot, resolveDockerBuildContext, now, resolvedTarget.RepoPath, resolvedTarget.Environment, target.VersionOverride)
		if err != nil {
			return nil, err
		}
	}

	specs, err := resolveDeploySpecsForContexts(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, resolvedTarget, deployContexts, target.VersionOverride, currentBuild, runtimeImageOverride)
	if err != nil {
		return nil, err
	}
	specs, err = appendRuntimeFallbackSpecs(ctx, store, resolvedTarget, target.VersionOverride, target.RuntimeChartOverride, specs, runtimeSelected, hasLocalRuntime, runtimeImageOverride != "")
	if err != nil {
		return nil, err
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("deploy: no components selected for %s/%s — pass --components with a chart name, save a default selection, or select the runtime to bootstrap the environment", resolvedTarget.Tenant, resolvedTarget.Environment)
	}
	return specs, nil
}

// resolveGuardedDeploySelection resolves the deploy component selection,
// traces the tier it came from, and refuses when the selection cannot be
// resolved correctly here — an in-pod runtime-only fallback that cannot see the
// saved selection (see guardInPodBlindRuntimeOnlySelection) or a saved selection
// that shadows a richer repo plan (see guardSavedSelectionShadowingPlan) — the
// ordering both the local-repo and sourceless deploy paths share.
func resolveGuardedDeploySelection(ctx core.Context, target DeployTarget, resolvedTarget configpkg.OpenResult, plan configpkg.ProjectK8sConfig) ([]string, error) {
	selected, selectionSource := resolveSelectedDeployComponents(target.Components, resolvedTarget.EnvConfig.Deploy.Components, plan)
	if err := guardInPodBlindRuntimeOnlySelection(os.Getenv, resolvedTarget, target, selected, selectionSource); err != nil {
		return nil, err
	}
	traceDeployComponentSelection(ctx, selected, selectionSource)
	missing := traceSavedSelectionShadowingPlan(ctx, selected, selectionSource, plan)
	if err := guardSavedSelectionShadowingPlan(missing, selected, resolvedTarget.Tenant, resolvedTarget.Environment); err != nil {
		return nil, err
	}
	return selected, nil
}

func traceDeployComponentSelection(ctx core.Context, selected []string, source string) {
	if len(selected) == 0 {
		ctx.Trace("deploy: component selection source " + source + "; deploying the runtime chart alone")
		return
	}
	ctx.Trace("deploy: component selection source " + source + "; components " + strings.Join(selected, ", "))
}

// traceSavedSelectionShadowingPlan reports when a saved deploy.components
// selection wins the precedence over a repo k8s.deployments plan that names
// more than the saved selection does, and returns the names the plan asks for
// beyond the saved set (nil when there is no shadowing). Selection tiers still
// never merge; the caller turns a non-empty result into a refusal
// (guardSavedSelectionShadowingPlan) rather than silently deploying the stale
// subset — a plain `erun deploy` used to do exactly that, which is how
// frs-oci-registry sat a whole release behind before anyone happened to
// compare chart versions by hand instead of checking health (erun#1712).
func traceSavedSelectionShadowingPlan(ctx core.Context, selected []string, source string, plan configpkg.ProjectK8sConfig) []string {
	if source != deploySelectionSourceSaved {
		return nil
	}
	var missing []string
	for _, name := range planComponentNameList(plan) {
		if !slices.Contains(selected, name) {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	ctx.Trace("deploy: saved components shadow the repo plan; plan also names " + strings.Join(missing, ", "))
	return missing
}

// guardSavedSelectionShadowingPlan turns a non-empty shadow (see
// traceSavedSelectionShadowingPlan) into a refusal. Code has no way to tell a
// saved selection that simply predates a plan addition from one an operator
// narrowed on purpose forever, so it never guesses either way: it does not
// widen the deploy to include what the plan now names (that could resurrect a
// component an operator deliberately removed), and it does not proceed on the
// stale subset either (that is the silent-strand bug this guards against). It
// names both remedies instead and lets the operator pick. --components has its
// own, higher-precedence tier and is resolved before a saved selection ever
// applies, so an explicit one-shot narrowing is never blocked by this guard.
func guardSavedSelectionShadowingPlan(missing, selected []string, tenant, environment string) error {
	if len(missing) == 0 {
		return nil
	}
	adopted := append(append([]string{}, selected...), missing...)
	return fmt.Errorf("deploy: saved deploy.components for %s/%s omits %s, which the repo k8s.deployments plan names; "+
		"a plain deploy refuses rather than silently leave it behind. Adopt it with `erun init %s %s --components %s`, "+
		"return to the plan with `erun init %s %s --components ''`, or pass --components explicitly this run to bypass the saved selection",
		tenant, environment, strings.Join(missing, ", "),
		tenant, environment, strings.Join(adopted, ","),
		tenant, environment)
}

// resolvePublishedDeploySpecs resolves a sourceless deploy for a target whose
// repo is not local (a runtime or remote-agent env): every selected chart is
// installed by reference from the published registry — each platform component
// via its published erun-<component> chart (top-level, no local umbrella) and
// the runtime via the published erun-devops chart. Selection comes from
// --components, then the env's saved deploy.components, then the repo
// k8s.deployments plan (see resolvePublishedDeployPlan for how that plan is
// located on this machine). An empty selection deploys the runtime alone
// (bootstrap/heal), matching the prior published-runtime-only behaviour.
func resolvePublishedDeploySpecs(ctx core.Context, store DeployStore, findProjectRoot shared.ProjectFinderFunc, resolvedTarget configpkg.OpenResult, target DeployTarget) ([]DeploySpec, error) {
	plan, err := resolvePublishedDeployPlan(ctx, findProjectRoot, resolvedTarget)
	if err != nil {
		return nil, err
	}
	selected, err := resolveGuardedDeploySelection(ctx, target, resolvedTarget, plan)
	if err != nil {
		return nil, err
	}

	tenantComponents := SelectedPublishableComponents(selected, resolvedTarget.Tenant, plan)
	runtimeSelected := deploySelectionIncludesRuntime(selected, resolvedTarget.Tenant)

	// Deploying the tenant's own component charts binds the whole deploy to the
	// tenant's version line: the tenant runtime chart (<tenant>-devops) and every
	// selected component chart must be published at this version. Verify up front so
	// an incoherent version fails fast — the runtime must not silently fall back to
	// the shared erun-devops chart, and a component missing at the version must not
	// half-apply before a mid-rollout chart pull aborts the deploy. An erun-only /
	// bootstrap deploy (no tenant components) keeps the published-fallback path.
	if len(tenantComponents) > 0 {
		concretized, err := ensureTenantChartsPublishedFromDeployRegistry(ctx, resolvedTarget, target.VersionOverride, runtimeSelected, tenantComponents)
		if err != nil {
			return nil, err
		}
		resolvedTarget = concretized
	}

	specs, err := resolvePublishedComponentDeploySpecs(ctx, store, resolvedTarget, tenantComponents, target.VersionOverride)
	if err != nil {
		return nil, err
	}
	specs, err = appendPublishedRuntimeDeploySpecs(ctx, store, resolvedTarget, target, runtimeSelected, specs)
	if err != nil {
		return nil, err
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("deploy: no components selected for %s/%s — pass --components with a publishable component, save a default selection, or select the runtime to bootstrap the environment", resolvedTarget.Tenant, resolvedTarget.Environment)
	}
	return specs, nil
}

// resolvePublishedComponentDeploySpecs resolves the by-reference deploy spec
// for each selected tenant component chart.
func resolvePublishedComponentDeploySpecs(ctx core.Context, store DeployStore, resolvedTarget configpkg.OpenResult, tenantComponents []string, versionOverride string) ([]DeploySpec, error) {
	specs := make([]DeploySpec, 0, len(tenantComponents))
	for _, component := range tenantComponents {
		spec, err := resolvePublishedComponentDeploySpec(ctx, resolvedTarget, component, versionOverride)
		if err != nil {
			return nil, err
		}
		if err := configureDeployInputMetadata(store, resolvedTarget, &spec.Deploy); err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

// appendPublishedRuntimeDeploySpecs appends the published runtime chart's
// deploy spec(s) when the selection includes the runtime; otherwise it
// returns specs unchanged.
func appendPublishedRuntimeDeploySpecs(ctx core.Context, store DeployStore, resolvedTarget configpkg.OpenResult, target DeployTarget, runtimeSelected bool, specs []DeploySpec) ([]DeploySpec, error) {
	if !runtimeSelected {
		return specs, nil
	}
	runtimeImageExplicit := strings.TrimSpace(target.RuntimeImageOverride) != ""
	runtimeSpecs, err := resolvePublishedDevopsDeploySpecs(ctx, store, resolvedTarget, target.VersionOverride, target.RuntimeChartOverride, runtimeImageExplicit)
	if err != nil {
		return nil, err
	}
	return append(specs, runtimeSpecs...), nil
}

// ensureTenantChartsPublishedFromDeployRegistry concretizes target's registries
// (a cluster: entry only resolves to a real host/insecure marker here — the
// runtime-chart-only path never needed this, since its own candidate ladder
// already tolerates an unconcretized entry) so tenant component charts (and
// the tenant's own runtime chart) verify against the DEPLOY registry `erun
// push` actually publishes them to, then runs that verification. Returns the
// concretized target so the caller's subsequent chart resolution reuses it.
func ensureTenantChartsPublishedFromDeployRegistry(ctx core.Context, target configpkg.OpenResult, versionOverride string, runtimeSelected bool, tenantComponents []string) (configpkg.OpenResult, error) {
	target, err := kube.ConcretizeDeployTargetRegistries(ctx, target)
	if err != nil {
		return configpkg.OpenResult{}, err
	}
	if err := ensureTenantChartsPublished(ctx, target, versionOverride, runtimeSelected, tenantComponents); err != nil {
		return configpkg.OpenResult{}, err
	}
	return target, nil
}

// appendRuntimeFallbackSpecs appends the published erun-devops runtime spec when
// the runtime is selected but the tenant has no repo-local runtime chart — the
// deploy counterpart of erun open's published fallback, letting a plain deploy
// bootstrap or heal the runtime from the published ERun image.
func appendRuntimeFallbackSpecs(ctx core.Context, store DeployStore, resolvedTarget configpkg.OpenResult, versionOverride, runtimeChartOverride string, specs []DeploySpec, runtimeSelected, hasLocalRuntime, runtimeImageExplicit bool) ([]DeploySpec, error) {
	if !runtimeSelected || hasLocalRuntime {
		return specs, nil
	}
	ctx.Trace("deploy: runtime selected with no repo-local runtime chart; installing the published " + configpkg.DevopsComponentName + " chart")
	publishedSpecs, err := resolvePublishedDevopsDeploySpecs(ctx, store, resolvedTarget, versionOverride, runtimeChartOverride, runtimeImageExplicit)
	if err != nil {
		return nil, err
	}
	return append(specs, publishedSpecs...), nil
}

func resolveDeploySpecsForContexts(ctx core.Context, store DeployStore, findProjectRoot shared.ProjectFinderFunc, resolveDockerBuildContext shared.BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now shared.NowFunc, target configpkg.OpenResult, deployContexts []shared.KubernetesDeployContext, versionOverride string, currentBuild *shared.DockerBuildSpec, runtimeImageOverride string) ([]DeploySpec, error) {
	specs := make([]DeploySpec, 0, len(deployContexts))
	for _, deployContext := range deployContexts {
		spec, err := resolveDeploySpecForContext(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, deployContext, versionOverride, currentBuild, runtimeImageOverride)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

// resolvePublishedDevopsDeploySpecs resolves the single published-devops deploy
// spec for a target with no local k8s tree to install — a remote-repo target, or
// a local env bootstrapping on --runtime-image before its own <tenant>-devops
// chart exists. runtimeChartOverride is the operator's --runtime-chart value
// when one is coming (applyRuntimeChartOverride applies it afterward).
// runtimeImageExplicit is true when the caller's runtime image came from an
// operator --runtime-image on this invocation, not merely a persisted memo.
func resolvePublishedDevopsDeploySpecs(ctx core.Context, store DeployStore, resolvedTarget configpkg.OpenResult, versionOverride, runtimeChartOverride string, runtimeImageExplicit bool) ([]DeploySpec, error) {
	spec, err := ResolvePublishedDevopsDeploySpecWithOverrides(ctx, resolvedTarget, versionOverride, runtimeChartOverride, runtimeImageExplicit)
	if err != nil {
		return nil, err
	}
	if err := configureDeployInputMetadata(store, resolvedTarget, &spec.Deploy); err != nil {
		return nil, err
	}
	return []DeploySpec{spec}, nil
}

// resolveCurrentLocalDeployContexts discovers, filters, and orders the local k8s
// deploy contexts for the target's repo. A missing or empty <tenant>-devops/k8s
// tree is not an error: it yields an empty set, and the caller stands the runtime
// up via the published erun-devops chart. Any other discovery error (a malformed
// chart) still propagates.
func resolveCurrentLocalDeployContexts(findProjectRoot shared.ProjectFinderFunc, resolveKubernetesDeployContext DeployContextResolverFunc, resolvedTarget configpkg.OpenResult, selected []string, plan configpkg.ProjectK8sConfig) ([]shared.KubernetesDeployContext, error) {
	deployContexts, err := ResolveCurrentKubernetesDeployContexts(findProjectRoot, resolveKubernetesDeployContext, resolvedTarget.RepoPath)
	if err != nil {
		if !isNoLocalDeployChartsError(err) {
			return nil, err
		}
		deployContexts = nil
	}
	deployContexts, err = filterDeployContextsBySelection(deployContexts, selected, resolvedTarget.Tenant)
	if err != nil {
		return nil, err
	}
	sortDeployContextsByDeployOrder(deployContexts, plan)
	return deployContexts, nil
}

// isNoLocalDeployChartsError reports whether a discovery error means "this repo
// has no local deploy charts to install" — either the <tenant>-devops/k8s tree
// is absent (fs.ErrNotExist) or present but empty of charts (the "helm chart
// not found in current directory" sentinel ResolveCurrentKubernetesDeployContexts
// returns). Both are tolerated so the runtime can still deploy via the published
// erun-devops chart; a malformed-chart error is not this class and propagates.
func isNoLocalDeployChartsError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	return err.Error() == "helm chart not found in current directory"
}

func resolveDeploySpecForOpenResult(ctx core.Context, store DeployStore, findProjectRoot shared.ProjectFinderFunc, resolveDockerBuildContext shared.BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now shared.NowFunc, target configpkg.OpenResult, componentName, versionOverride string, currentBuild *shared.DockerBuildSpec) (DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	now = freezeNow(now)

	deployContext, err := resolveDeployContextForTarget(findProjectRoot, resolveKubernetesDeployContext, target, componentName)
	if err != nil {
		return DeploySpec{}, err
	}

	return resolveDeploySpecForContext(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, deployContext, versionOverride, currentBuild, "")
}

func resolveDeploySpecForContext(ctx core.Context, store DeployStore, findProjectRoot shared.ProjectFinderFunc, resolveDockerBuildContext shared.BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now shared.NowFunc, target configpkg.OpenResult, deployContext shared.KubernetesDeployContext, versionOverride string, currentBuild *shared.DockerBuildSpec, runtimeImageOverride string) (DeploySpec, error) {
	// A pure deploy installs by reference and only consults the store; the other
	// dependencies are kept on the signature for the shared resolution contract.
	store, _, _, _, _ = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	target = applyDeployKubernetesContext(store, target)
	target, err := kube.ConcretizeDeployTargetRegistries(ctx, target)
	if err != nil {
		return DeploySpec{}, err
	}

	// Build-orchestration path: a build --deploy / open --deploy / UI run has
	// already built and pushed the working-tree image and hands it to deploy by
	// reference; deploy installs it via an ImageOverride and never builds it.
	if currentBuild != nil && deployContextOwnsDockerBuild(deployContext, *currentBuild) {
		return resolveDeploySpecForCurrentDockerBuild(store, target, deployContext, *currentBuild)
	}

	// Pure deploy: install the image + chart already published at a version,
	// by reference. A version is a content identity minted by build/push;
	// deploy never builds or synthesizes one. The version comes from the
	// explicit override or, for a redeploy of the env's current version, the
	// persisted RuntimeVersion (the CLI --current switch). With neither, the
	// operator has not said what to deploy — that is an error, not a trigger
	// to build the working tree.
	version := strings.TrimSpace(versionOverride)
	versionFromPersist := false
	if version == "" {
		version = strings.TrimSpace(target.EnvConfig.RuntimeVersion)
		if version != "" {
			versionFromPersist = true
		}
	}
	if version == "" {
		return DeploySpec{}, fmt.Errorf("deploy: a version is required — pass a version produced by `erun build`/`erun push`, or `--current` to redeploy the version this environment already runs")
	}
	// A runtime-image override lets an operator stand up a new env on the shared
	// ERun base image before the tenant's own <tenant>-devops image exists.
	if runtimeImageOverride != "" && deployContextOwnsRuntimeChart(deployContext, target.Tenant) {
		return ResolvePublishedDevopsDeploySpecWithReason(ctx, target, version, "bypassing the repo-local runtime chart for the runtime image override "+runtimeImageOverride, "", true)
	}
	return resolveInstallExistingVersionDeploySpec(ctx, store, target, deployContext, version, versionFromPersist)
}
