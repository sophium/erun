package components

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartref"
	planpkg "github.com/sophium/erun/erun-common/primitive/deploy/plan"
	targetpkg "github.com/sophium/erun/erun-common/primitive/deploy/target"
	"github.com/sophium/erun/erun-common/primitive/shared/opentarget"
)

// componentChartPrefix derives from RuntimeReleaseName to stay in lockstep with runtime chart naming.
func componentChartPrefix(tenant string) string {
	return opentarget.TenantResourcePrefix(tenant)
}

// publishablePlatformComponentNames are the tenant-prefixed charts a sourceless env can deploy by reference.
func publishablePlatformComponentNames(tenant string) []string {
	prefix := componentChartPrefix(tenant)
	names := make([]string, len(planpkg.DeployComponentBaseOrder))
	for i, base := range planpkg.DeployComponentBaseOrder {
		names[i] = prefix + "-" + base
	}
	return names
}

// SelectedPublishableComponents returns selected non-runtime components in plan or default order.
func SelectedPublishableComponents(selected []string, tenant string, plan config.ProjectK8sConfig) []string {
	runtimeAliases := chartref.RuntimeComponentNames(tenant)
	out := make([]string, 0, len(selected))
	for _, name := range selected {
		if slices.Contains(runtimeAliases, name) {
			continue
		}
		out = append(out, name)
	}
	rank := planpkg.ComponentRankByPlan(plan)
	sort.SliceStable(out, func(i, j int) bool {
		return rank(out[i]) < rank(out[j])
	})
	return out
}

const (
	deploySelectionSourceFlag    = "--components flag"
	DeploySelectionSourceSaved   = "saved deploy.components"
	DeploySelectionSourcePlan    = "k8s.deployments plan"
	DeploySelectionSourceDefault = "default (runtime only)"
)

// ResolveSelectedDeployComponents takes the highest non-empty tier (flag, saved, plan); tiers never
// merge. Empty means the runtime chart alone.
func ResolveSelectedDeployComponents(flagComponents, savedComponents []string, plan config.ProjectK8sConfig) ([]string, string) {
	if names := NormalizeComponentNames(flagComponents); len(names) > 0 {
		return names, deploySelectionSourceFlag
	}
	if names := NormalizeComponentNames(savedComponents); len(names) > 0 {
		return names, DeploySelectionSourceSaved
	}
	if names := PlanComponentNameList(plan); len(names) > 0 {
		return names, DeploySelectionSourcePlan
	}
	return nil, DeploySelectionSourceDefault
}

func NormalizeComponentNames(components []string) []string {
	out := make([]string, 0, len(components))
	seen := make(map[string]struct{}, len(components))
	for _, raw := range components {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func PlanComponentNameList(plan config.ProjectK8sConfig) []string {
	names := make([]string, 0)
	for _, step := range plan.Deployments {
		names = append(names, step.Components...)
	}
	return NormalizeComponentNames(names)
}

// An empty selection deploys the runtime alone (bootstrap/heal).
func FilterDeployContextsBySelection(contexts []model.KubernetesDeployContext, selected []string, tenant string) ([]model.KubernetesDeployContext, error) {
	if err := validateSelectedDeployComponents(selected, contexts, tenant); err != nil {
		return nil, err
	}
	runtimeSelected := DeploySelectionIncludesRuntime(selected, tenant)
	out := make([]model.KubernetesDeployContext, 0, len(contexts))
	for _, deployContext := range contexts {
		if chartref.DeployContextOwnsRuntimeChart(deployContext, tenant) {
			if runtimeSelected {
				out = append(out, deployContext)
			}
			continue
		}
		if slices.Contains(selected, strings.TrimSpace(deployContext.ComponentName)) {
			out = append(out, deployContext)
		}
	}
	return out, nil
}

func DeploySelectionIncludesRuntime(selected []string, tenant string) bool {
	if len(selected) == 0 {
		return true
	}
	for _, alias := range chartref.RuntimeComponentNames(tenant) {
		if slices.Contains(selected, alias) {
			return true
		}
	}
	return false
}

func ContainsRuntimeContext(contexts []model.KubernetesDeployContext, tenant string) bool {
	for _, deployContext := range contexts {
		if chartref.DeployContextOwnsRuntimeChart(deployContext, tenant) {
			return true
		}
	}
	return false
}

// Runtime aliases stay valid without a local chart; they resolve to the published erun-devops.
func validateSelectedDeployComponents(selected []string, contexts []model.KubernetesDeployContext, tenant string) error {
	if len(selected) == 0 {
		return nil
	}
	valid := deployableComponentNameSet(contexts, tenant)
	for _, name := range selected {
		if _, ok := valid[name]; !ok {
			return fmt.Errorf("unknown deploy component %q; valid components for this environment are: %s", name, strings.Join(sortedNameSet(valid), ", "))
		}
	}
	return nil
}

func deployableComponentNameSet(contexts []model.KubernetesDeployContext, tenant string) map[string]struct{} {
	valid := make(map[string]struct{}, len(contexts)+2)
	for _, deployContext := range contexts {
		if name := strings.TrimSpace(deployContext.ComponentName); name != "" {
			valid[name] = struct{}{}
		}
	}
	for _, alias := range chartref.RuntimeComponentNames(tenant) {
		valid[alias] = struct{}{}
	}
	return valid
}

func sortedNameSet(set map[string]struct{}) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DeployableComponent is one selectable deploy target for an environment.
type DeployableComponent struct {
	// Name is a chart directory under <tenant>-devops/k8s/, or the runtime release name.
	Name    string `json:"name"`
	Runtime bool   `json:"runtime"`
	// Source is "local-chart" or, for a runtime with no local chart, "published-chart".
	Source string `json:"source"`
	// Selected reports membership in the env's resolved default selection.
	Selected bool `json:"selected"`
	// PublishedChart is the chart a by-reference deploy installs at the chosen version; empty until
	// a version-aware transport resolves it.
	PublishedChart string `json:"publishedChart,omitempty"`
}

const deployComponentSourcePublished = "published-chart"

// DeployComponentSourcePublished is the Source of a component installed by reference.
const DeployComponentSourcePublished = deployComponentSourcePublished

// ResolvedRuntimeChartName returns the tenant's <tenant>-devops chart when published, else erun-devops.
// It only labels the picker; deploy walks runtimeChartCandidates.
func ResolvedRuntimeChartName(tenant string, tenantChartPublished bool) string {
	chart := config.RuntimeReleaseName(tenant)
	if chart != config.DevopsComponentName && tenantChartPublished {
		return chart
	}
	return config.DevopsComponentName
}

// ResolveDeployableComponents lists the published component charts plus the runtime for a deploy.
// The version, not the env's source, decides which charts exist. It never builds or pushes.
func ResolveDeployableComponents(store seam.DeployStore, findProjectRoot seam.ProjectFinderFunc, resolveDockerBuildContext seam.BuildContextResolverFunc, resolveKubernetesDeployContext seam.DeployContextResolverFunc, now seam.NowFunc, target model.DeployTarget) ([]DeployableComponent, error) {
	store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now = targetpkg.NormalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	now = targetpkg.FreezeNow(now)

	resolvedTarget, err := targetpkg.ResolveDeployTarget("", false, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
	if err != nil {
		return nil, err
	}
	tenant := resolvedTarget.Tenant
	runtimeName := config.RuntimeReleaseName(tenant)

	selected, _ := ResolveSelectedDeployComponents(target.Components, resolvedTarget.EnvConfig.Deploy.Components, config.ProjectK8sConfig{})
	runtimeSelected := DeploySelectionIncludesRuntime(selected, tenant)

	platformNames := publishablePlatformComponentNames(tenant)
	platform := make(map[string]struct{}, len(platformNames))
	components := make([]DeployableComponent, 0, len(platformNames)+len(selected)+1)
	// Runtime first, so a scrollable picker cannot hide the primary target.
	components = append(components, DeployableComponent{
		Name:     runtimeName,
		Runtime:  true,
		Source:   deployComponentSourcePublished,
		Selected: runtimeSelected,
	})
	for _, name := range platformNames {
		platform[name] = struct{}{}
		components = append(components, DeployableComponent{
			Name:     name,
			Source:   deployComponentSourcePublished,
			Selected: slices.Contains(selected, name),
		})
	}
	// Surface tenant-published charts beyond the platform set that this env selects.
	runtimeAliases := chartref.RuntimeComponentNames(tenant)
	for _, name := range selected {
		if _, ok := platform[name]; ok {
			continue
		}
		if slices.Contains(runtimeAliases, name) {
			continue
		}
		components = append(components, DeployableComponent{
			Name:     name,
			Source:   deployComponentSourcePublished,
			Selected: true,
		})
	}
	return components, nil
}
