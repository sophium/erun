package eruncommon

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// postgresComponentName is the chart that owns the environment's Postgres
// instance. Deploy treats it specially: a pending database reset forces its
// helm step even when every image was promoted from the fingerprint cache.
const postgresComponentName = "erun-backend-postgres"

// optInDeployComponents lists charts that are not deployed by default and must
// be explicitly included via DeployTarget.Components or the deploy --components
// flag. Other charts (notably the per-tenant runtime chart) are always
// deployed when present.
var optInDeployComponents = []string{
	postgresComponentName,
	"erun-backend-db",
	"erun-backend-api",
}

// defaultDeployComponentOrder is the fallback rank used when the project
// config has no k8s.deployments plan. Postgres → db → api; other components
// (e.g. the runtime chart) sort to the end so backend dependencies come up
// first.
var defaultDeployComponentOrder = []string{
	postgresComponentName,
	"erun-backend-db",
	"erun-backend-api",
}

// filterDeployContextsByComponents drops charts whose ComponentName is in the
// opt-in set unless that name is explicitly included — either by the
// --components flag or by being named in the project's k8s.deployments plan.
// Listing a chart in the plan is an implicit opt-in: a user who configures
// `environments.<env>.k8s.deployments: [..., erun-backend-api]` should get
// that chart deployed without also having to pass --components on every run.
// Unknown component names (not in the opt-in set) are rejected with an error
// so typos surface early instead of silently deploying nothing extra.
func filterDeployContextsByComponents(contexts []KubernetesDeployContext, components []string, plan ProjectK8sConfig) ([]KubernetesDeployContext, error) {
	requested, err := normalizeRequestedComponents(components)
	if err != nil {
		return nil, err
	}
	planned := planComponentNames(plan)
	out := make([]KubernetesDeployContext, 0, len(contexts))
	for _, deployContext := range contexts {
		name := strings.TrimSpace(deployContext.ComponentName)
		if isOptInDeployComponent(name) {
			if _, byFlag := requested[name]; byFlag {
				// included via --components
			} else if _, byPlan := planned[name]; byPlan {
				// included via project k8s.deployments
			} else {
				continue
			}
		}
		out = append(out, deployContext)
	}
	return out, nil
}

func planComponentNames(plan ProjectK8sConfig) map[string]struct{} {
	if len(plan.Deployments) == 0 {
		return nil
	}
	out := make(map[string]struct{})
	for _, step := range plan.Deployments {
		for _, name := range step.Components {
			out[strings.TrimSpace(name)] = struct{}{}
		}
	}
	return out
}

func isOptInDeployComponent(name string) bool {
	return slices.Contains(optInDeployComponents, name)
}

func normalizeRequestedComponents(components []string) (map[string]struct{}, error) {
	out := make(map[string]struct{}, len(components))
	for _, raw := range components {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if !isOptInDeployComponent(name) {
			return nil, fmt.Errorf("unknown deploy component %q; valid opt-in components are: %s", name, strings.Join(optInDeployComponents, ", "))
		}
		out[name] = struct{}{}
	}
	return out, nil
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
		groups := make([][]DeploySpec, 0, len(specs))
		for _, spec := range specs {
			groups = append(groups, []DeploySpec{spec})
		}
		return groups
	}
	specsByName := make(map[string]DeploySpec, len(specs))
	stepIndex := make(map[string]int)
	for i, step := range plan.Deployments {
		for _, name := range step.Components {
			stepIndex[strings.TrimSpace(name)] = i
		}
	}
	var trailing []DeploySpec
	for _, spec := range specs {
		name := strings.TrimSpace(spec.DeployContext.ComponentName)
		if _, planned := stepIndex[name]; planned {
			specsByName[name] = spec
			continue
		}
		trailing = append(trailing, spec)
	}
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

