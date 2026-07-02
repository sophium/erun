package eruncommon

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// postgresComponentName is the chart that owns the environment's Postgres
// instance; it deploys before the charts that depend on it (db migrations,
// PowerDNS) in the default deploy order.
const postgresComponentName = "erun-backend-postgres"

// powerdnsComponentName is the platform PowerDNS authoritative nameserver
// singleton. It runs the gpgsql backend against the shared
// erun-backend-postgres instance, so it must deploy after postgres.
const powerdnsComponentName = "erun-powerdns"

// defaultDeployComponentOrder is the fallback rank used when the project
// config has no k8s.deployments plan. Postgres → db → api → powerdns; other
// components (e.g. the runtime chart) sort to the end so backend dependencies
// come up first. It governs ordering only — not which components deploy.
var defaultDeployComponentOrder = []string{
	postgresComponentName,
	"erun-backend-db",
	"erun-backend-api",
	powerdnsComponentName,
}

// Sources reported by resolveSelectedDeployComponents so the dry-run trace
// names which precedence tier decided the selection.
const (
	deploySelectionSourceFlag    = "--components flag"
	deploySelectionSourceSaved   = "saved deploy.components"
	deploySelectionSourcePlan    = "k8s.deployments plan"
	deploySelectionSourceDefault = "default (runtime only)"
)

// resolveSelectedDeployComponents applies the strict-precedence tiers that
// decide which components deploy: the explicit --components flag wins; then the
// per-machine saved set (EnvConfig.deploy.components); then the repo
// k8s.deployments plan. Tiers do not merge — the highest non-empty tier fully
// determines the selection, matching "opt in for exactly that". An empty result
// (no flag, no saved set, no plan) means "no explicit selection"; the caller
// then defaults to the runtime chart alone (bootstrap/heal). The second return
// value names the tier for the dry-run trace.
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

// normalizeComponentNames trims, drops blanks, and de-duplicates a component
// name list, preserving first-seen order.
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

// planComponentNameList flattens the project k8s plan steps into an ordered,
// de-duplicated component name list (plan step order, names within a step in
// declaration order).
func planComponentNameList(plan ProjectK8sConfig) []string {
	names := make([]string, 0)
	for _, step := range plan.Deployments {
		names = append(names, step.Components...)
	}
	return normalizeComponentNames(names)
}

// filterDeployContextsBySelection keeps the discovered local chart contexts the
// operator selected — opt-in-only: a chart deploys iff its name is in the
// selection. The runtime chart is kept when it is named (by its <tenant>-devops
// release name or the erun-devops alias) or when the selection is empty (the
// bootstrap/heal default deploys the runtime alone). Selection names that match
// no discovered chart and no runtime alias are rejected so a typo or a stale
// saved entry fails loudly instead of silently deploying nothing.
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

// deploySelectionIncludesRuntime reports whether the runtime chart is part of a
// selection. An empty selection defaults to the runtime alone; a non-empty
// selection includes the runtime only when it names a runtime alias
// (<tenant>-devops or erun-devops).
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

// containsRuntimeContext reports whether any of the resolved local contexts is
// the runtime chart, so the caller knows a repo-local runtime chart backs the
// deploy (and no published fallback is needed).
func containsRuntimeContext(contexts []KubernetesDeployContext, tenant string) bool {
	for _, deployContext := range contexts {
		if deployContextOwnsRuntimeChart(deployContext, tenant) {
			return true
		}
	}
	return false
}

// validateSelectedDeployComponents fails when a selected name matches neither a
// discovered local chart nor a runtime alias. Runtime aliases are always valid
// (even with no local runtime chart) because they resolve to the published
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

// deployableComponentNameSet returns the names that may be selected for an
// environment: every discovered local chart directory name plus the runtime
// aliases (<tenant>-devops and erun-devops).
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

// ResolveDeployableComponents lists the deployable components for an
// environment: the local component charts discovered under <tenant>-devops/k8s
// plus the runtime item (a local <tenant>-devops/erun-devops chart if present,
// otherwise the published erun-devops chart). Selected reflects the env's
// current resolved default selection, so a transport can render the checklist
// pre-populated exactly as an equivalent `erun deploy` (with no --components)
// would resolve. It reads only — it never builds, pushes, or requires a
// version — and is the single source both CLI help and the desktop consume.
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
		// No repo-local runtime chart: the runtime deploys via the published
		// erun-devops chart. Offer it as the runtime item so the operator can
		// bootstrap/heal the env from the checklist.
		components = append(components, DeployableComponent{
			Name:     runtimeName,
			Runtime:  true,
			Source:   deployComponentSourcePublished,
			Selected: runtimeSelected,
		})
	}
	return components, nil
}

// sortDeployContextsByDeployOrder reorders contexts according to the given
// project k8s plan (or the hardcoded fallback if the plan is empty). The sort
// is stable so contexts not mentioned in the plan keep their relative input
// order at the end of the list.
func sortDeployContextsByDeployOrder(contexts []KubernetesDeployContext, plan ProjectK8sConfig) {
	rank := componentRankByPlan(plan)
	sort.SliceStable(contexts, func(i, j int) bool {
		return rank(contexts[i].ComponentName) < rank(contexts[j].ComponentName)
	})
}

// componentRankByPlan returns the rank function used for ordering. When the
// plan declares steps, components in step i have rank i; components not in
// any step rank at the end.
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

// groupDeploySpecsByPlan slots each resolved spec into a step. Specs whose
// component is mentioned in the same plan step end up in the same group;
// specs not mentioned in the plan each get their own trailing group,
// preserving input order. With an empty plan, each spec is its own step
// (strictly serial deploys, matching the pre-config behavior).
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

// serialDeploySpecGroups puts each spec in its own step, preserving input
// order — the empty-plan grouping (strictly serial deploys).
func serialDeploySpecGroups(specs []DeploySpec) [][]DeploySpec {
	groups := make([][]DeploySpec, 0, len(specs))
	for _, spec := range specs {
		groups = append(groups, []DeploySpec{spec})
	}
	return groups
}

// partitionDeploySpecsByPlan splits the specs into those whose component is
// named in the plan (keyed by component name) and those that are not (the
// trailing specs, in input order). It relies on the plan having at least one
// deployment step.
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
