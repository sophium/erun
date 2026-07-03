package eruncommon

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

const postgresComponentName = "erun-backend-postgres"

// powerdnsComponentName must deploy after postgres: PowerDNS uses the shared
// erun-backend-postgres instance as its backend store.
const powerdnsComponentName = "erun-powerdns"

// defaultDeployComponentOrder orders backend charts before their dependents;
// it governs ordering only, not which components deploy.
var defaultDeployComponentOrder = []string{
	postgresComponentName,
	"erun-backend-db",
	"erun-backend-api",
	powerdnsComponentName,
}

const (
	deploySelectionSourceFlag    = "--components flag"
	deploySelectionSourceSaved   = "saved deploy.components"
	deploySelectionSourcePlan    = "k8s.deployments plan"
	deploySelectionSourceDefault = "default (runtime only)"
)

// resolveSelectedDeployComponents takes the selection from the highest non-empty
// precedence tier (flag, then saved set, then repo plan); tiers never merge. An
// empty result means no explicit selection, which the caller treats as the runtime
// chart alone (bootstrap/heal).
func resolveSelectedDeployComponents(flagComponents, savedComponents []string, plan ProjectK8sConfig) ([]string, string) {
	if names := normalizeComponentNames(flagComponents); len(names) > 0 {
		return names, deploySelectionSourceFlag
	}
	if names := normalizeComponentNames(savedComponents); len(names) > 0 {
		return names, deploySelectionSourceSaved
	}
	if names := planComponentNameList(plan); len(names) > 0 {
		return names, deploySelectionSourcePlan
	}
	return nil, deploySelectionSourceDefault
}

func normalizeComponentNames(components []string) []string {
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

func planComponentNameList(plan ProjectK8sConfig) []string {
	names := make([]string, 0)
	for _, step := range plan.Deployments {
		names = append(names, step.Components...)
	}
	return normalizeComponentNames(names)
}

// filterDeployContextsBySelection is opt-in-only: a chart deploys iff its name is
// in the selection, and an empty selection deploys the runtime alone
// (bootstrap/heal).
func filterDeployContextsBySelection(contexts []KubernetesDeployContext, selected []string, tenant string) ([]KubernetesDeployContext, error) {
	if err := validateSelectedDeployComponents(selected, contexts, tenant); err != nil {
		return nil, err
	}
	runtimeSelected := deploySelectionIncludesRuntime(selected, tenant)
	out := make([]KubernetesDeployContext, 0, len(contexts))
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

// containsRuntimeContext tells the caller whether a repo-local runtime chart backs
// the deploy, or whether the published erun-devops fallback is needed.
func containsRuntimeContext(contexts []KubernetesDeployContext, tenant string) bool {
	for _, deployContext := range contexts {
		if deployContextOwnsRuntimeChart(deployContext, tenant) {
			return true
		}
	}
	return false
}

// validateSelectedDeployComponents rejects unknown component names. Runtime aliases
// stay valid even with no local runtime chart because they resolve to the published
// erun-devops chart.
func validateSelectedDeployComponents(selected []string, contexts []KubernetesDeployContext, tenant string) error {
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

func deployableComponentNameSet(contexts []KubernetesDeployContext, tenant string) map[string]struct{} {
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

// DeployableComponent describes one selectable deploy target for an
// environment. Transports that let the operator choose what `erun deploy` rolls
// out (the desktop "Components to deploy" checklist) render a list of these.
type DeployableComponent struct {
	// Name is the canonical selector: a chart directory name under
	// <tenant>-devops/k8s/, or the runtime release name for the runtime item.
	Name string `json:"name"`
	// Runtime marks the environment's runtime chart item.
	Runtime bool `json:"runtime"`
	// Source is "local-chart" when a repo-local chart backs the component, or
	// "published-chart" for the runtime when only the published erun-devops
	// chart is available (no local <tenant>-devops/erun-devops chart).
	Source string `json:"source"`
	// Selected reports whether the component is in the env's current resolved
	// default selection (saved deploy.components, else the repo plan, else —
	// when both are empty — the runtime alone).
	Selected bool `json:"selected"`
}

const (
	deployComponentSourceLocal     = "local-chart"
	deployComponentSourcePublished = "published-chart"
)

// ResolveDeployableComponents lists an environment's deployable components with
// Selected reflecting what a plain `erun deploy` would roll out, so a transport can
// render the checklist pre-populated. It reads only: it never builds, pushes, or
// requires a version, and is the single source both CLI help and the desktop consume.
func ResolveDeployableComponents(store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target DeployTarget) ([]DeployableComponent, error) {
	store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	now = freezeNow(now)

	resolvedTarget, err := resolveDeployTarget(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
	if err != nil {
		return nil, err
	}
	tenant := resolvedTarget.Tenant
	runtimeName := RuntimeReleaseName(tenant)

	var contexts []KubernetesDeployContext
	if !resolvedTarget.RemoteRepo() {
		contexts, err = ResolveCurrentKubernetesDeployContexts(findProjectRoot, resolveKubernetesDeployContext, resolvedTarget.RepoPath)
		if err != nil && !isNoLocalDeployChartsError(err) {
			return nil, err
		}
	}

	plan, err := loadProjectK8sPlanForRepo(resolvedTarget.RepoPath, resolvedTarget.Environment)
	if err != nil {
		return nil, err
	}
	selected, _ := resolveSelectedDeployComponents(target.Components, resolvedTarget.EnvConfig.Deploy.Components, plan)
	sortDeployContextsByDeployOrder(contexts, plan)
	runtimeSelected := deploySelectionIncludesRuntime(selected, tenant)

	components := make([]DeployableComponent, 0, len(contexts)+1)
	hasLocalRuntime := false
	for _, deployContext := range contexts {
		if deployContextOwnsRuntimeChart(deployContext, tenant) {
			hasLocalRuntime = true
			components = append(components, DeployableComponent{
				Name:     runtimeName,
				Runtime:  true,
				Source:   deployComponentSourceLocal,
				Selected: runtimeSelected,
			})
			continue
		}
		name := strings.TrimSpace(deployContext.ComponentName)
		components = append(components, DeployableComponent{
			Name:     name,
			Source:   deployComponentSourceLocal,
			Selected: slices.Contains(selected, name),
		})
	}
	if !hasLocalRuntime {
		// No repo-local runtime chart: offer the published erun-devops chart as the
		// runtime item so the operator can still bootstrap/heal the env.
		components = append(components, DeployableComponent{
			Name:     runtimeName,
			Runtime:  true,
			Source:   deployComponentSourcePublished,
			Selected: runtimeSelected,
		})
	}
	return components, nil
}

// sortDeployContextsByDeployOrder ranks contexts by the k8s plan (hardcoded fallback
// when the plan is empty); the sort must stay stable so plan-less contexts keep their
// input order at the end.
func sortDeployContextsByDeployOrder(contexts []KubernetesDeployContext, plan ProjectK8sConfig) {
	rank := componentRankByPlan(plan)
	sort.SliceStable(contexts, func(i, j int) bool {
		return rank(contexts[i].ComponentName) < rank(contexts[j].ComponentName)
	})
}

func componentRankByPlan(plan ProjectK8sConfig) func(name string) int {
	if len(plan.Deployments) == 0 {
		rank := make(map[string]int, len(defaultDeployComponentOrder))
		for i, name := range defaultDeployComponentOrder {
			rank[name] = i
		}
		fallback := len(defaultDeployComponentOrder)
		return func(name string) int {
			if r, ok := rank[strings.TrimSpace(name)]; ok {
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

func groupDeploySpecsByPlan(specs []DeploySpec, plan ProjectK8sConfig) [][]DeploySpec {
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

func partitionDeploySpecsByPlan(specs []DeploySpec, plan ProjectK8sConfig) (map[string]DeploySpec, []DeploySpec) {
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
