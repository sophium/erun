package spec

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
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/buildtarget"
	"github.com/sophium/erun/erun-common/base/seam/deploytarget"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
	"github.com/sophium/erun/erun-common/base/seam/imagebuild"
	"github.com/sophium/erun/erun-common/base/seam/projectroot"
	"github.com/sophium/erun/erun-common/infra/kube"
	registrypkg "github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartref"
	"github.com/sophium/erun/erun-common/primitive/deploy/components"
	"github.com/sophium/erun/erun-common/primitive/deploy/kubecheck"
	"github.com/sophium/erun/erun-common/primitive/deploy/mcpauth"
	planpkg "github.com/sophium/erun/erun-common/primitive/deploy/plan"
	"github.com/sophium/erun/erun-common/primitive/deploy/published"
	targetpkg "github.com/sophium/erun/erun-common/primitive/deploy/target"
)

func ResolveDeploySpec(ctx core.Context, store deploytarget.DeployStore, findProjectRoot projectroot.ProjectFinderFunc, resolveDockerBuildContext buildtarget.BuildContextResolverFunc, resolveKubernetesDeployContext deploytarget.DeployContextResolverFunc, now buildtarget.NowFunc, target model.DeployTarget, componentName, versionOverride string) (helmdeploy.DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, _, now = targetpkg.NormalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	versionOverride = targetpkg.ResolveDeployVersionOverride(target, versionOverride)

	resolvedTarget, err := targetpkg.ResolveDeployTarget(ctx.Command, ctx.CommandScopesTenantByFlag, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
	if err != nil {
		return helmdeploy.DeploySpec{}, err
	}
	spec, err := resolveDeploySpecForOpenResult(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, resolvedTarget, componentName, versionOverride, nil)
	if err != nil {
		return helmdeploy.DeploySpec{}, err
	}
	override, ok, err := resolveRolloutTimeoutOverride(target.RolloutTimeout)
	if err != nil {
		return helmdeploy.DeploySpec{}, err
	}
	if ok {
		spec.Deploy.Timeout = override
	}
	if !target.NamespaceQuotaOverride.IsZero() {
		if err := configpkg.ValidateNamespaceResourceQuota(target.NamespaceQuotaOverride); err != nil {
			return helmdeploy.DeploySpec{}, fmt.Errorf("namespace quota override: %w", err)
		}
		spec.Deploy.NamespaceQuota = target.NamespaceQuotaOverride
	}
	specs := []helmdeploy.DeploySpec{spec}
	if err := finalizeRuntimeChartSpecs(ctx, target, resolvedTarget, specs); err != nil {
		return helmdeploy.DeploySpec{}, err
	}
	return specs[0], nil
}

// ResolveCurrentDeploySpecs resolves specs for `erun deploy`, which installs by reference and never builds.
func ResolveCurrentDeploySpecs(ctx core.Context, store deploytarget.DeployStore, findProjectRoot projectroot.ProjectFinderFunc, resolveDockerBuildContext buildtarget.BuildContextResolverFunc, resolveKubernetesDeployContext deploytarget.DeployContextResolverFunc, now buildtarget.NowFunc, target model.DeployTarget) ([]helmdeploy.DeploySpec, error) {
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

// resolveRolloutTimeoutOverride validates a per-deploy rollout timeout; a malformed
// value is a hard error. It beats env deploy.timeout and DefaultHelmDeploymentTimeout.
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

// ResolveCurrentDeploySpecsOrchestrated serves both `erun deploy` and `build --deploy`;
// with buildOrchestration it references the image the build phase already pushed.
func ResolveCurrentDeploySpecsOrchestrated(ctx core.Context, store deploytarget.DeployStore, findProjectRoot projectroot.ProjectFinderFunc, resolveDockerBuildContext buildtarget.BuildContextResolverFunc, resolveKubernetesDeployContext deploytarget.DeployContextResolverFunc, now buildtarget.NowFunc, target model.DeployTarget, buildOrchestration bool) ([]helmdeploy.DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, _, now = targetpkg.NormalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	now = targetpkg.FreezeNow(now)

	resolvedTarget, err := targetpkg.ResolveDeployTarget(ctx.Command, ctx.CommandScopesTenantByFlag, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
	if err != nil {
		return nil, err
	}

	runtimeImageOverride := strings.TrimSpace(target.RuntimeImageOverride)
	if runtimeImageOverride != "" {
		// Persist the override so a later open/redeploy uses it even over a repo-local runtime chart.
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

// finalizeRuntimeChartSpecs is the single seam every resolve path funnels through
// so runtime-chart guards and overrides apply to all of them.
func finalizeRuntimeChartSpecs(ctx core.Context, target model.DeployTarget, resolvedTarget configpkg.OpenResult, specs []helmdeploy.DeploySpec) error {
	for i := range specs {
		applyRuntimeChartOverride(ctx, target, &specs[i])
		if err := mcpauth.ApplyMCPAuthToRuntimeSpec(ctx, target, &specs[i]); err != nil {
			return err
		}
		if err := mcpauth.GuardMCPAuthDowngrade(ctx, target, specs[i].Deploy); err != nil {
			return err
		}
	}
	return guardInPodRuntimeDeploy(os.Getenv, resolvedTarget, specs)
}

func resolveDeploySpecsForResolvedTarget(ctx core.Context, store deploytarget.DeployStore, findProjectRoot projectroot.ProjectFinderFunc, resolveDockerBuildContext buildtarget.BuildContextResolverFunc, resolveKubernetesDeployContext deploytarget.DeployContextResolverFunc, now buildtarget.NowFunc, resolvedTarget configpkg.OpenResult, target model.DeployTarget, buildOrchestration bool, runtimeImageOverride string) ([]helmdeploy.DeploySpec, error) {
	// Configure registry auth before any chart probe so probes are not refused as anonymous.
	registrypkg.ConfigureInPodDeclaredRegistryAuth(ctx, resolvedTarget)
	if resolvedTarget.RemoteRepo() {
		return resolvePublishedDeploySpecs(ctx, store, findProjectRoot, resolvedTarget, target)
	}
	return resolveSelectedLocalDeploySpecs(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, resolvedTarget, target, buildOrchestration, runtimeImageOverride)
}

// resolveSelectedLocalDeploySpecs deploys exactly the selected charts; an empty
// selection means the runtime alone, via the published chart when none is vendored.
func resolveSelectedLocalDeploySpecs(ctx core.Context, store deploytarget.DeployStore, findProjectRoot projectroot.ProjectFinderFunc, resolveDockerBuildContext buildtarget.BuildContextResolverFunc, resolveKubernetesDeployContext deploytarget.DeployContextResolverFunc, now buildtarget.NowFunc, resolvedTarget configpkg.OpenResult, target model.DeployTarget, buildOrchestration bool, runtimeImageOverride string) ([]helmdeploy.DeploySpec, error) {
	plan, err := planpkg.LoadProjectK8sPlanForRepo(resolvedTarget.RepoPath, resolvedTarget.Environment)
	if err != nil {
		return nil, err
	}
	selected, err := resolveGuardedDeploySelection(ctx, target, resolvedTarget, plan)
	if err != nil {
		return nil, err
	}
	kubecheck.TraceConfiguredDeployPaths(ctx, resolvedTarget.RepoPath)

	deployContexts, err := resolveCurrentLocalDeployContexts(findProjectRoot, resolveKubernetesDeployContext, resolvedTarget, selected, plan)
	if err != nil {
		return nil, err
	}
	runtimeSelected := components.DeploySelectionIncludesRuntime(selected, resolvedTarget.Tenant)
	hasLocalRuntime := components.ContainsRuntimeContext(deployContexts, resolvedTarget.Tenant)

	var currentBuild *imagebuild.DockerBuildSpec
	if buildOrchestration && resolvedTarget.EnvConfig.BuildsHere() {
		// Reference the built image by tag: install-by-reference would verify a tag the
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

func resolveGuardedDeploySelection(ctx core.Context, target model.DeployTarget, resolvedTarget configpkg.OpenResult, plan configpkg.ProjectK8sConfig) ([]string, error) {
	selected, selectionSource := components.ResolveSelectedDeployComponents(target.Components, resolvedTarget.EnvConfig.Deploy.Components, plan)
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

// traceSavedSelectionShadowingPlan returns the plan components a saved
// deploy.components selection leaves out, or nil.
func traceSavedSelectionShadowingPlan(ctx core.Context, selected []string, source string, plan configpkg.ProjectK8sConfig) []string {
	if source != components.DeploySelectionSourceSaved {
		return nil
	}
	var missing []string
	for _, name := range components.PlanComponentNameList(plan) {
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

// guardSavedSelectionShadowingPlan refuses rather than guess whether a stale saved
// selection was narrowed on purpose; widening could resurrect a removed component.
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

// resolvePublishedDeploySpecs installs every selected chart by reference from the
// published registry, for a target whose repo is not local.
func resolvePublishedDeploySpecs(ctx core.Context, store deploytarget.DeployStore, findProjectRoot projectroot.ProjectFinderFunc, resolvedTarget configpkg.OpenResult, target model.DeployTarget) ([]helmdeploy.DeploySpec, error) {
	plan, err := planpkg.ResolvePublishedDeployPlan(ctx, findProjectRoot, resolvedTarget)
	if err != nil {
		return nil, err
	}
	selected, err := resolveGuardedDeploySelection(ctx, target, resolvedTarget, plan)
	if err != nil {
		return nil, err
	}

	tenantComponents := components.SelectedPublishableComponents(selected, resolvedTarget.Tenant, plan)
	runtimeSelected := components.DeploySelectionIncludesRuntime(selected, resolvedTarget.Tenant)

	// Tenant components bind the deploy to the tenant's version line: verify every
	// chart up front so a mismatch fails before anything half-applies.
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

func resolvePublishedComponentDeploySpecs(ctx core.Context, store deploytarget.DeployStore, resolvedTarget configpkg.OpenResult, tenantComponents []string, versionOverride string) ([]helmdeploy.DeploySpec, error) {
	specs := make([]helmdeploy.DeploySpec, 0, len(tenantComponents))
	for _, component := range tenantComponents {
		spec, err := published.ResolvePublishedComponentDeploySpec(ctx, resolvedTarget, component, versionOverride)
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

func appendPublishedRuntimeDeploySpecs(ctx core.Context, store deploytarget.DeployStore, resolvedTarget configpkg.OpenResult, target model.DeployTarget, runtimeSelected bool, specs []helmdeploy.DeploySpec) ([]helmdeploy.DeploySpec, error) {
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

// ensureTenantChartsPublishedFromDeployRegistry concretizes cluster: registries so
// tenant charts verify against the registry `erun push` publishes to.
func ensureTenantChartsPublishedFromDeployRegistry(ctx core.Context, target configpkg.OpenResult, versionOverride string, runtimeSelected bool, tenantComponents []string) (configpkg.OpenResult, error) {
	target, err := kube.ConcretizeDeployTargetRegistries(ctx, target)
	if err != nil {
		return configpkg.OpenResult{}, err
	}
	if err := published.EnsureTenantChartsPublished(ctx, target, versionOverride, runtimeSelected, tenantComponents); err != nil {
		return configpkg.OpenResult{}, err
	}
	return target, nil
}

func appendRuntimeFallbackSpecs(ctx core.Context, store deploytarget.DeployStore, resolvedTarget configpkg.OpenResult, versionOverride, runtimeChartOverride string, specs []helmdeploy.DeploySpec, runtimeSelected, hasLocalRuntime, runtimeImageExplicit bool) ([]helmdeploy.DeploySpec, error) {
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

func resolveDeploySpecsForContexts(ctx core.Context, store deploytarget.DeployStore, findProjectRoot projectroot.ProjectFinderFunc, resolveDockerBuildContext buildtarget.BuildContextResolverFunc, resolveKubernetesDeployContext deploytarget.DeployContextResolverFunc, now buildtarget.NowFunc, target configpkg.OpenResult, deployContexts []model.KubernetesDeployContext, versionOverride string, currentBuild *imagebuild.DockerBuildSpec, runtimeImageOverride string) ([]helmdeploy.DeploySpec, error) {
	specs := make([]helmdeploy.DeploySpec, 0, len(deployContexts))
	for _, deployContext := range deployContexts {
		spec, err := resolveDeploySpecForContext(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, deployContext, versionOverride, currentBuild, runtimeImageOverride)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

// runtimeImageExplicit is true only for an operator --runtime-image on this
// invocation, not a persisted memo.
func resolvePublishedDevopsDeploySpecs(ctx core.Context, store deploytarget.DeployStore, resolvedTarget configpkg.OpenResult, versionOverride, runtimeChartOverride string, runtimeImageExplicit bool) ([]helmdeploy.DeploySpec, error) {
	spec, err := published.ResolvePublishedDevopsDeploySpecWithOverrides(ctx, resolvedTarget, versionOverride, runtimeChartOverride, runtimeImageExplicit)
	if err != nil {
		return nil, err
	}
	if err := configureDeployInputMetadata(store, resolvedTarget, &spec.Deploy); err != nil {
		return nil, err
	}
	return []helmdeploy.DeploySpec{spec}, nil
}

// A missing or empty k8s tree is not an error: the runtime then comes from the published chart.
func resolveCurrentLocalDeployContexts(findProjectRoot projectroot.ProjectFinderFunc, resolveKubernetesDeployContext deploytarget.DeployContextResolverFunc, resolvedTarget configpkg.OpenResult, selected []string, plan configpkg.ProjectK8sConfig) ([]model.KubernetesDeployContext, error) {
	deployContexts, err := kubecheck.ResolveCurrentKubernetesDeployContexts(findProjectRoot, resolveKubernetesDeployContext, resolvedTarget.RepoPath)
	if err != nil {
		if !isNoLocalDeployChartsError(err) {
			return nil, err
		}
		deployContexts = nil
	}
	deployContexts, err = components.FilterDeployContextsBySelection(deployContexts, selected, resolvedTarget.Tenant)
	if err != nil {
		return nil, err
	}
	planpkg.SortDeployContextsByDeployOrder(deployContexts, plan)
	return deployContexts, nil
}

func isNoLocalDeployChartsError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	return err.Error() == "helm chart not found in current directory"
}

func resolveDeploySpecForOpenResult(ctx core.Context, store deploytarget.DeployStore, findProjectRoot projectroot.ProjectFinderFunc, resolveDockerBuildContext buildtarget.BuildContextResolverFunc, resolveKubernetesDeployContext deploytarget.DeployContextResolverFunc, now buildtarget.NowFunc, target configpkg.OpenResult, componentName, versionOverride string, currentBuild *imagebuild.DockerBuildSpec) (helmdeploy.DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now = targetpkg.NormalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	now = targetpkg.FreezeNow(now)

	deployContext, err := targetpkg.ResolveDeployContextForTarget(findProjectRoot, resolveKubernetesDeployContext, target, componentName)
	if err != nil {
		return helmdeploy.DeploySpec{}, err
	}

	return resolveDeploySpecForContext(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, deployContext, versionOverride, currentBuild, "")
}

func resolveDeploySpecForContext(ctx core.Context, store deploytarget.DeployStore, findProjectRoot projectroot.ProjectFinderFunc, resolveDockerBuildContext buildtarget.BuildContextResolverFunc, resolveKubernetesDeployContext deploytarget.DeployContextResolverFunc, now buildtarget.NowFunc, target configpkg.OpenResult, deployContext model.KubernetesDeployContext, versionOverride string, currentBuild *imagebuild.DockerBuildSpec, runtimeImageOverride string) (helmdeploy.DeploySpec, error) {
	store, _, _, _, _ = targetpkg.NormalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	target = applyDeployKubernetesContext(store, target)
	target, err := kube.ConcretizeDeployTargetRegistries(ctx, target)
	if err != nil {
		return helmdeploy.DeploySpec{}, err
	}

	if currentBuild != nil && deployContextOwnsDockerBuild(deployContext, *currentBuild) {
		return resolveDeploySpecForCurrentDockerBuild(store, target, deployContext, *currentBuild)
	}

	// A pure deploy never builds or synthesizes a version; without an explicit or
	// current version there is nothing to deploy.
	version := strings.TrimSpace(versionOverride)
	versionFromPersist := false
	if version == "" {
		version = strings.TrimSpace(target.EnvConfig.RuntimeVersion)
		if version != "" {
			versionFromPersist = true
		}
	}
	if version == "" {
		return helmdeploy.DeploySpec{}, fmt.Errorf("deploy: a version is required — pass a version produced by `erun build`/`erun push`, or `--current` to redeploy the version this environment already runs")
	}
	if runtimeImageOverride != "" && chartref.DeployContextOwnsRuntimeChart(deployContext, target.Tenant) {
		return published.ResolvePublishedDevopsDeploySpecWithReason(ctx, target, version, "bypassing the repo-local runtime chart for the runtime image override "+runtimeImageOverride, "", true)
	}
	return resolveInstallExistingVersionDeploySpec(ctx, store, target, deployContext, version, versionFromPersist)
}
