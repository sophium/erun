package deploy

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// deployComponentBaseOrder orders component chart bases so backend charts precede dependents;
// oci-registry follows backend-api because it trusts that signer's key. It governs order only.
var deployComponentBaseOrder = []string{
	"backend-postgres",
	"backend-db",
	"backend-api",
	"oci-registry",
	"powerdns",
	"zitadel",
	"docs",
}

// canonicalChartPrefix prefixes erun's published charts and is the umbrella subchart value scope.
const canonicalChartPrefix = "erun"

// componentChartPrefix derives from RuntimeReleaseName to stay in lockstep with runtime chart naming.
func componentChartPrefix(tenant string) string {
	return shared.TenantResourcePrefix(tenant)
}

// publishablePlatformComponentNames are the tenant-prefixed charts a sourceless env can deploy by reference.
func publishablePlatformComponentNames(tenant string) []string {
	prefix := componentChartPrefix(tenant)
	names := make([]string, len(deployComponentBaseOrder))
	for i, base := range deployComponentBaseOrder {
		names[i] = prefix + "-" + base
	}
	return names
}

func componentBaseName(name string) string {
	if _, base, ok := strings.Cut(strings.TrimSpace(name), "-"); ok {
		return base
	}
	return strings.TrimSpace(name)
}

// SelectedPublishableComponents returns selected non-runtime components in plan or default order.
func SelectedPublishableComponents(selected []string, tenant string, plan config.ProjectK8sConfig) []string {
	runtimeAliases := runtimeComponentNames(tenant)
	out := make([]string, 0, len(selected))
	for _, name := range selected {
		if slices.Contains(runtimeAliases, name) {
			continue
		}
		out = append(out, name)
	}
	rank := componentRankByPlan(plan)
	sort.SliceStable(out, func(i, j int) bool {
		return rank(out[i]) < rank(out[j])
	})
	return out
}

const (
	deploySelectionSourceFlag    = "--components flag"
	deploySelectionSourceSaved   = "saved deploy.components"
	deploySelectionSourcePlan    = "k8s.deployments plan"
	deploySelectionSourceDefault = "default (runtime only)"
)

// resolveSelectedDeployComponents takes the highest non-empty tier (flag, saved, plan); tiers never
// merge. Empty means the runtime chart alone.
func resolveSelectedDeployComponents(flagComponents, savedComponents []string, plan config.ProjectK8sConfig) ([]string, string) {
	if names := NormalizeComponentNames(flagComponents); len(names) > 0 {
		return names, deploySelectionSourceFlag
	}
	if names := NormalizeComponentNames(savedComponents); len(names) > 0 {
		return names, deploySelectionSourceSaved
	}
	if names := planComponentNameList(plan); len(names) > 0 {
		return names, deploySelectionSourcePlan
	}
	return nil, deploySelectionSourceDefault
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

func planComponentNameList(plan config.ProjectK8sConfig) []string {
	names := make([]string, 0)
	for _, step := range plan.Deployments {
		names = append(names, step.Components...)
	}
	return NormalizeComponentNames(names)
}

// An empty selection deploys the runtime alone (bootstrap/heal).
func filterDeployContextsBySelection(contexts []model.KubernetesDeployContext, selected []string, tenant string) ([]model.KubernetesDeployContext, error) {
	if err := validateSelectedDeployComponents(selected, contexts, tenant); err != nil {
		return nil, err
	}
	runtimeSelected := deploySelectionIncludesRuntime(selected, tenant)
	out := make([]model.KubernetesDeployContext, 0, len(contexts))
	for _, deployContext := range contexts {
		if deployContextOwnsRuntimeChart(deployContext, tenant) {
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

func deploySelectionIncludesRuntime(selected []string, tenant string) bool {
	if len(selected) == 0 {
		return true
	}
	for _, alias := range runtimeComponentNames(tenant) {
		if slices.Contains(selected, alias) {
			return true
		}
	}
	return false
}

func containsRuntimeContext(contexts []model.KubernetesDeployContext, tenant string) bool {
	for _, deployContext := range contexts {
		if deployContextOwnsRuntimeChart(deployContext, tenant) {
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
	for _, alias := range runtimeComponentNames(tenant) {
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
func ResolveDeployableComponents(store DeployStore, findProjectRoot shared.ProjectFinderFunc, resolveDockerBuildContext shared.BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now shared.NowFunc, target model.DeployTarget) ([]DeployableComponent, error) {
	store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	now = freezeNow(now)

	resolvedTarget, err := resolveDeployTarget("", false, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
	if err != nil {
		return nil, err
	}
	tenant := resolvedTarget.Tenant
	runtimeName := config.RuntimeReleaseName(tenant)

	selected, _ := resolveSelectedDeployComponents(target.Components, resolvedTarget.EnvConfig.Deploy.Components, config.ProjectK8sConfig{})
	runtimeSelected := deploySelectionIncludesRuntime(selected, tenant)

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
	runtimeAliases := runtimeComponentNames(tenant)
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

// The sort must stay stable so plan-less contexts keep their input order.
func sortDeployContextsByDeployOrder(contexts []model.KubernetesDeployContext, plan config.ProjectK8sConfig) {
	rank := componentRankByPlan(plan)
	sort.SliceStable(contexts, func(i, j int) bool {
		return rank(contexts[i].ComponentName) < rank(contexts[j].ComponentName)
	})
}

func componentRankByPlan(plan config.ProjectK8sConfig) func(name string) int {
	if len(plan.Deployments) == 0 {
		rank := make(map[string]int, len(deployComponentBaseOrder))
		for i, base := range deployComponentBaseOrder {
			rank[base] = i
		}
		fallback := len(deployComponentBaseOrder)
		return func(name string) int {
			if r, ok := rank[componentBaseName(name)]; ok {
				return r
			}
			return fallback
		}
	}
	rank := make(map[string]int)
	for i, step := range plan.Deployments {
		for _, name := range step.Components {
			rank[strings.TrimSpace(name)] = i
		}
	}
	fallback := len(plan.Deployments)
	return func(name string) int {
		if r, ok := rank[strings.TrimSpace(name)]; ok {
			return r
		}
		return fallback
	}
}

func groupDeploySpecsByPlan(specs []DeploySpec, plan config.ProjectK8sConfig) [][]DeploySpec {
	if len(specs) == 0 {
		return nil
	}
	if len(plan.Deployments) == 0 {
		return serialDeploySpecGroups(specs)
	}
	specsByName, trailing := partitionDeploySpecsByPlan(specs, plan)
	out := make([][]DeploySpec, 0, len(plan.Deployments)+len(trailing))
	for _, step := range plan.Deployments {
		group := make([]DeploySpec, 0, len(step.Components))
		for _, name := range step.Components {
			if spec, ok := specsByName[strings.TrimSpace(name)]; ok {
				group = append(group, spec)
			}
		}
		if len(group) > 0 {
			out = append(out, group)
		}
	}
	for _, spec := range trailing {
		out = append(out, []DeploySpec{spec})
	}
	return out
}

func serialDeploySpecGroups(specs []DeploySpec) [][]DeploySpec {
	groups := make([][]DeploySpec, 0, len(specs))
	for _, spec := range specs {
		groups = append(groups, []DeploySpec{spec})
	}
	return groups
}

func partitionDeploySpecsByPlan(specs []DeploySpec, plan config.ProjectK8sConfig) (map[string]DeploySpec, []DeploySpec) {
	stepIndex := make(map[string]int)
	for i, step := range plan.Deployments {
		for _, name := range step.Components {
			stepIndex[strings.TrimSpace(name)] = i
		}
	}
	specsByName := make(map[string]DeploySpec, len(specs))
	var trailing []DeploySpec
	for _, spec := range specs {
		name := strings.TrimSpace(spec.DeployContext.ComponentName)
		if _, planned := stepIndex[name]; planned {
			specsByName[name] = spec
			continue
		}
		trailing = append(trailing, spec)
	}
	return specsByName, trailing
}
