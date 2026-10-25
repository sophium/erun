package main

import (
	"os"
	"strings"
	"sync"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	componentspkg "github.com/sophium/erun/erun-common/primitive/deploy/components"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// LoadDeployComponents lists what an equivalent `erun deploy` would roll out, limited
// to component charts published at the deploy version. Read-only.
func (a *App) LoadDeployComponents(selection uimodel.Selection) (uiDeployComponents, error) {
	selection = uimodel.NormalizeSelection(selection)
	tenant := strings.TrimSpace(selection.Tenant)
	environment := strings.TrimSpace(selection.Environment)
	if err := uimodel.ErrMissingTenantOrEnvironment("load deploy components", tenant, environment); err != nil {
		return uiDeployComponents{}, err
	}
	components, err := componentspkg.ResolveDeployableComponents(
		a.deps.store,
		a.deps.findProjectRoot,
		nil,
		nil,
		nil,
		model.DeployTarget{Tenant: tenant, Environment: environment},
	)
	if err != nil {
		return uiDeployComponents{}, err
	}
	filtered := a.filterDeployComponentsByChartAvailability(tenant, environment, selection.Version, components)
	// Resolved now so a version with no chart is reported before committing to a rollout.
	version := strings.TrimSpace(selection.Version)
	if version == "" {
		if env, _, envErr := a.deps.store.LoadEnvConfig(tenant, environment); envErr == nil {
			version = strings.TrimSpace(env.RuntimeVersion)
		}
	}
	return uiDeployComponents{
		Components:   filtered,
		RuntimeChart: a.resolveRuntimeChartPlan(tenant, environment, version, runtimeIsLocalChart(filtered)),
	}, nil
}

// runtimeIsLocalChart reports whether the runtime chart is in the worktree, bypassing registries.
func runtimeIsLocalChart(components []componentspkg.DeployableComponent) bool {
	for _, component := range components {
		if component.Runtime {
			return component.Source != componentspkg.DeployComponentSourcePublished
		}
	}
	return false
}

// filterDeployComponentsByChartAvailability drops component charts not published at
// the deploy version. The runtime item is always kept; a missing one fails at deploy time.
func (a *App) filterDeployComponentsByChartAvailability(tenant, environment, version string, components []componentspkg.DeployableComponent) []componentspkg.DeployableComponent {
	env, _, err := a.deps.store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return components
	}
	version = strings.TrimSpace(version)
	if version == "" {
		version = strings.TrimSpace(env.RuntimeVersion)
	}
	if version == "" {
		return components
	}

	runtimeChart := config.RuntimeReleaseName(tenant)
	probe := make([]string, 0, len(components)+1)
	for _, component := range components {
		if !component.Runtime {
			probe = append(probe, component.Name)
		}
	}
	// Probe the tenant's runtime chart so the row names <tenant>-devops or the erun-devops fallback.
	if runtimeChart != config.DevopsComponentName {
		probe = append(probe, runtimeChart)
	}
	available := a.publishedComponentChartAvailability(tenant, environment, version, probe)
	resolvedRuntimeChart := componentspkg.ResolvedRuntimeChartName(tenant, available[runtimeChart])

	filtered := make([]componentspkg.DeployableComponent, 0, len(components))
	for _, component := range components {
		if component.Runtime {
			component.PublishedChart = resolvedRuntimeChart
			filtered = append(filtered, component)
			continue
		}
		if available[component.Name] {
			filtered = append(filtered, component)
		}
	}
	return filtered
}

// publishedComponentChartAvailability probes charts/<component> at version in parallel.
// ERUN_CHART_AVAILABILITY_OVERRIDE replaces the probe for the headless suite only.
func (a *App) publishedComponentChartAvailability(tenant, environment, version string, components []string) map[string]bool {
	if override, ok := chartAvailabilityOverride(version); ok {
		return override
	}
	ctx := a.context()
	namespace := strings.TrimSpace(a.runtimeRegistryNamespace(tenant, environment))
	if namespace == "" {
		namespace = config.DefaultContainerRegistry
	}

	result := make(map[string]bool, len(components))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, name := range components {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		wg.Add(1)
		go func(component string) {
			defer wg.Done()
			versions, regErr := a.deps.resolveImageRegistry(ctx, namespace, "charts/"+component)
			published := regErr == nil && versions.HasVersion(version)
			mu.Lock()
			result[component] = published
			mu.Unlock()
		}(name)
	}
	wg.Wait()
	return result
}

// chartAvailabilityOverride parses "1.0.112=erun-backend-api,erun-docs;1.0.106=";
// when set, unlisted versions have no published components.
func chartAvailabilityOverride(version string) (map[string]bool, bool) {
	raw := strings.TrimSpace(os.Getenv("ERUN_CHART_AVAILABILITY_OVERRIDE"))
	if raw == "" {
		return nil, false
	}
	version = strings.TrimSpace(version)
	available := map[string]bool{}
	for _, entry := range strings.Split(raw, ";") {
		key, list, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(key) != version {
			continue
		}
		for _, component := range strings.Split(list, ",") {
			if component = strings.TrimSpace(component); component != "" {
				available[component] = true
			}
		}
		break
	}
	return available, true
}
