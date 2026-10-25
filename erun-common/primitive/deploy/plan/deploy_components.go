package plan

import (
	"sort"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
)

// DeployComponentBaseOrder orders component chart bases so backend charts precede dependents;
// oci-registry follows backend-api because it trusts that signer's key. It governs order only.
var DeployComponentBaseOrder = []string{
	"backend-postgres",
	"backend-db",
	"backend-api",
	"oci-registry",
	"powerdns",
	"zitadel",
	"docs",
}

func componentBaseName(name string) string {
	if _, base, ok := strings.Cut(strings.TrimSpace(name), "-"); ok {
		return base
	}
	return strings.TrimSpace(name)
}

// The sort must stay stable so plan-less contexts keep their input order.
func SortDeployContextsByDeployOrder(contexts []model.KubernetesDeployContext, plan config.ProjectK8sConfig) {
	rank := ComponentRankByPlan(plan)
	sort.SliceStable(contexts, func(i, j int) bool {
		return rank(contexts[i].ComponentName) < rank(contexts[j].ComponentName)
	})
}

func ComponentRankByPlan(plan config.ProjectK8sConfig) func(name string) int {
	if len(plan.Deployments) == 0 {
		rank := make(map[string]int, len(DeployComponentBaseOrder))
		for i, base := range DeployComponentBaseOrder {
			rank[base] = i
		}
		fallback := len(DeployComponentBaseOrder)
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

func GroupDeploySpecsByPlan(specs []helmdeploy.DeploySpec, plan config.ProjectK8sConfig) [][]helmdeploy.DeploySpec {
	if len(specs) == 0 {
		return nil
	}
	if len(plan.Deployments) == 0 {
		return serialDeploySpecGroups(specs)
	}
	specsByName, trailing := partitionDeploySpecsByPlan(specs, plan)
	out := make([][]helmdeploy.DeploySpec, 0, len(plan.Deployments)+len(trailing))
	for _, step := range plan.Deployments {
		group := make([]helmdeploy.DeploySpec, 0, len(step.Components))
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
		out = append(out, []helmdeploy.DeploySpec{spec})
	}
	return out
}

func serialDeploySpecGroups(specs []helmdeploy.DeploySpec) [][]helmdeploy.DeploySpec {
	groups := make([][]helmdeploy.DeploySpec, 0, len(specs))
	for _, spec := range specs {
		groups = append(groups, []helmdeploy.DeploySpec{spec})
	}
	return groups
}

func partitionDeploySpecsByPlan(specs []helmdeploy.DeploySpec, plan config.ProjectK8sConfig) (map[string]helmdeploy.DeploySpec, []helmdeploy.DeploySpec) {
	stepIndex := make(map[string]int)
	for i, step := range plan.Deployments {
		for _, name := range step.Components {
			stepIndex[strings.TrimSpace(name)] = i
		}
	}
	specsByName := make(map[string]helmdeploy.DeploySpec, len(specs))
	var trailing []helmdeploy.DeploySpec
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
