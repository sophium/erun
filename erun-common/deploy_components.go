package eruncommon

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// deployComponentBaseOrder is the tenant-agnostic base of each platform
// component chart, ordered so backend charts precede their dependents: powerdns
// deploys after backend-postgres (its backend store), and docs — a one-shot Job
// with no in-cluster dependency — orders last. A component's published chart is
// named <prefix>-<base> where prefix is the tenant's release base (see
// componentChartPrefix), e.g. erun-backend-postgres or frs-backend-api. Governs
// ordering only, not which components deploy.
var deployComponentBaseOrder = []string{
	"backend-postgres",
	"backend-db",
	"backend-api",
	"powerdns",
	"docs",
}

// componentChartPrefix is the registry prefix a tenant's component charts carry,
// matching the runtime release base: erun for the canonical product, <tenant>
// for a tenant that publishes its own charts (e.g. frs). Derived from
// RuntimeReleaseName so it stays in lockstep with the runtime chart naming.
func componentChartPrefix(tenant string) string {
	return strings.TrimSuffix(RuntimeReleaseName(tenant), "-devops")
}

// publishablePlatformComponentNames are the component charts published to the
// registry (oci://<registry>/charts/<name>) that a sourceless env — one with no
// local repo, i.e. a runtime or remote-agent env — can deploy by reference, no
// local umbrella chart required. The names are tenant-prefixed so a tenant that
// publishes its own charts (frs-backend-api) is offered them, not the canonical
// erun set; a transport filters to those actually published at the deploy version.
func publishablePlatformComponentNames(tenant string) []string {
	prefix := componentChartPrefix(tenant)
	names := make([]string, len(deployComponentBaseOrder))
	for i, base := range deployComponentBaseOrder {
		names[i] = prefix + "-" + base
	}
	return names
}

// componentBaseName strips the tenant/product prefix (the first "-" segment) so
// ordering ranks erun-backend-api and frs-backend-api by the same base.
func componentBaseName(name string) string {
	if _, base, ok := strings.Cut(strings.TrimSpace(name), "-"); ok {
		return base
	}
	return strings.TrimSpace(name)
}

// selectedPublishableComponents returns the selected non-runtime components in
// default-rank order (postgres → db → api → powerdns → docs) for the sourceless
// by-reference deploy path; the runtime is resolved separately.
func selectedPublishableComponents(selected []string, tenant string) []string {
	runtimeAliases := runtimeComponentNames(tenant)
	out := make([]string, 0, len(selected))
	for _, name := range selected {
		if slices.Contains(runtimeAliases, name) {
			continue
		}
		out = append(out, name)
	}
	rank := componentRankByPlan(ProjectK8sConfig{})
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

const deployComponentSourcePublished = "published-chart"

// ResolveDeployableComponents lists the published component charts a deploy of a
// chosen version can roll out — the canonical platform components plus the runtime,
// installed by reference — so a transport can render the checklist. The list is the
// same for every env type because the deploy version, not the env's local source,
// decides which charts exist; a transport filters it to those actually published at
// the selected version. It reads only: it never builds, pushes, or requires a version.
func ResolveDeployableComponents(store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target DeployTarget) ([]DeployableComponent, error) {
	store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	now = freezeNow(now)

	resolvedTarget, err := resolveDeployTarget(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
	if err != nil {
		return nil, err
	}
	tenant := resolvedTarget.Tenant
	runtimeName := RuntimeReleaseName(tenant)

	// The checklist lists what deploying a chosen version rolls out — the published
	// component charts (installed by reference) plus the runtime — the same for every
	// env type, because the version (not the env's local source) decides which charts
	// exist. A transport then filters this to the charts actually published at the
	// selected version. Deploying local working-tree charts by name stays available
	// to an operator via the CLI; the desktop checklist is a published-version view.
	selected, _ := resolveSelectedDeployComponents(target.Components, resolvedTarget.EnvConfig.Deploy.Components, ProjectK8sConfig{})
	runtimeSelected := deploySelectionIncludesRuntime(selected, tenant)

	platformNames := publishablePlatformComponentNames(tenant)
	platform := make(map[string]struct{}, len(platformNames))
	components := make([]DeployableComponent, 0, len(platformNames)+len(selected)+1)
	// Runtime first: it is the default-checked, primary deploy target, so it must
	// head the checklist rather than sit below the optional component charts (where
	// a scrollable picker can hide it).
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
	// A tenant publishes its own component charts (e.g. frs-backend-api) beyond the
	// fixed platform set; surface any this env selects so the checklist reflects
	// what deploying it rolls out. A transport filters to those actually published
	// at the chosen version.
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
		// Rank by tenant-agnostic base so erun-backend-api and frs-backend-api
		// share ordering; a name with no known base sorts last.
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
