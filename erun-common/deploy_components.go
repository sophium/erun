package eruncommon

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// optInDeployComponents lists charts that are not deployed by default and must
// be explicitly included via DeployTarget.Components or the deploy --components
// flag. Other charts (notably the per-tenant runtime chart) are always
// deployed when present.
var optInDeployComponents = []string{
	"erun-backend-postgres",
	"erun-backend-db",
	"erun-backend-api",
}

// deployComponentOrder defines the rank used when sorting charts that are
// resolved together in a single deploy. Lower rank deploys first. Charts not
// listed here keep their relative order at the end.
//
// Postgres comes up first so the migration job can connect; the migration
// job's post-install hook blocks helm until it succeeds, which gates the API
// rollout; the runtime pod is independent and rolls last.
var deployComponentOrder = []string{
	"erun-backend-postgres",
	"erun-backend-db",
	"erun-backend-api",
}

// filterDeployContextsByComponents drops charts whose ComponentName is in the
// opt-in set unless that name appears in components. Unknown component names
// (not in the opt-in set) are rejected with an error so typos surface early
// instead of silently deploying nothing extra.
func filterDeployContextsByComponents(contexts []KubernetesDeployContext, components []string) ([]KubernetesDeployContext, error) {
	requested, err := normalizeRequestedComponents(components)
	if err != nil {
		return nil, err
	}
	out := make([]KubernetesDeployContext, 0, len(contexts))
	for _, deployContext := range contexts {
		name := strings.TrimSpace(deployContext.ComponentName)
		if isOptInDeployComponent(name) {
			if _, ok := requested[name]; !ok {
				continue
			}
		}
		out = append(out, deployContext)
	}
	return out, nil
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

// sortDeployContextsByDeployOrder reorders contexts so listed components run
// first in the order defined by deployComponentOrder. Stable so charts not in
// the order list keep their relative input order.
func sortDeployContextsByDeployOrder(contexts []KubernetesDeployContext) {
	rank := make(map[string]int, len(deployComponentOrder))
	for i, name := range deployComponentOrder {
		rank[name] = i
	}
	defaultRank := len(deployComponentOrder)
	sort.SliceStable(contexts, func(i, j int) bool {
		ra, ok := rank[strings.TrimSpace(contexts[i].ComponentName)]
		if !ok {
			ra = defaultRank
		}
		rb, ok := rank[strings.TrimSpace(contexts[j].ComponentName)]
		if !ok {
			rb = defaultRank
		}
		return ra < rb
	})
}

// allDockerBuildsPromoted reports whether every build in the slice was marked
// for fingerprint promotion (cached fp-tag hit). An empty slice returns false:
// charts with no locally-built images (e.g. erun-backend-postgres referencing
// only the public postgres image) should keep deploying so chart-only changes
// (templates, values) still ship.
func allDockerBuildsPromoted(builds []DockerBuildSpec) bool {
	if len(builds) == 0 {
		return false
	}
	for _, build := range builds {
		if !build.Promote {
			return false
		}
	}
	return true
}
