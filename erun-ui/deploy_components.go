package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	eruncommon "github.com/sophium/erun/erun-common"
)

// LoadDeployComponents powers the Runtime tab's "Components to deploy" checklist
// so the operator sees and edits exactly what an equivalent `erun deploy` would
// roll out. Read-only: it never builds, pushes, or deploys.
//
// For a sourceless (RemoteRepo) env the checklist is version-aware: only the
// component charts actually published at the deploy version are offered, since
// deploying an unpublished chart would fail. The version is the selection's
// version-to-deploy when set, else the env's current runtime version.
func (a *App) LoadDeployComponents(selection uiSelection) ([]eruncommon.DeployableComponent, error) {
	selection = normalizeSelection(selection)
	tenant := strings.TrimSpace(selection.Tenant)
	environment := strings.TrimSpace(selection.Environment)
	if tenant == "" || environment == "" {
		return nil, fmt.Errorf("tenant and environment are required")
	}
	components, err := eruncommon.ResolveDeployableComponents(
		a.deps.store,
		a.deps.findProjectRoot,
		nil,
		nil,
		nil,
		eruncommon.DeployTarget{Tenant: tenant, Environment: environment},
	)
	if err != nil {
		return nil, err
	}
	return a.filterDeployComponentsByChartAvailability(tenant, environment, selection.Version, components), nil
}

// filterDeployComponentsByChartAvailability drops published component charts the
// registry has not published at the version this deploy would use, so a
// sourceless env's checklist offers only charts a deploy could actually pull. It
// applies only to sourceless (RemoteRepo) envs — a local-repo env deploys its
// own charts, always available — and always keeps the runtime item, whose chart
// exists at every deployable version (a genuinely missing one surfaces at deploy
// time as PublishedChartNotFoundError, not here).
func (a *App) filterDeployComponentsByChartAvailability(tenant, environment, version string, components []eruncommon.DeployableComponent) []eruncommon.DeployableComponent {
	env, _, err := a.deps.store.LoadEnvConfig(tenant, environment)
	if err != nil || !env.RemoteWorktree() {
		return components
	}
	version = strings.TrimSpace(version)
	if version == "" {
		version = strings.TrimSpace(env.RuntimeVersion)
	}
	if version == "" {
		// No version to probe against — don't hide anything.
		return components
	}

	probe := make([]string, 0, len(components))
	for _, component := range components {
		if !component.Runtime {
			probe = append(probe, component.Name)
		}
	}
	available := a.publishedComponentChartAvailability(tenant, environment, version, probe)

	filtered := make([]eruncommon.DeployableComponent, 0, len(components))
	for _, component := range components {
		if component.Runtime || available[component.Name] {
			filtered = append(filtered, component)
		}
	}
	return filtered
}

// publishedComponentChartAvailability reports, per component, whether its Helm
// chart is published at the given version in the env's chart registry. It reuses
// the injected image-registry resolver against the chart repo path
// (charts/<component>), probing components in parallel.
//
// ERUN_CHART_AVAILABILITY_OVERRIDE short-circuits the network probe with a fixed
// answer — a deterministic test seam for the headless UI suite, never set in
// production.
func (a *App) publishedComponentChartAvailability(tenant, environment, version string, components []string) map[string]bool {
	if override, ok := chartAvailabilityOverride(version); ok {
		return override
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	namespace := strings.TrimSpace(a.runtimeRegistryNamespace(tenant, environment))
	if namespace == "" {
		namespace = eruncommon.DefaultContainerRegistry
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

// chartAvailabilityOverride parses ERUN_CHART_AVAILABILITY_OVERRIDE, a headless-
// test seam of the form "1.0.112=erun-backend-api,erun-docs;1.0.106=" giving the
// published component charts per version. When set it is authoritative for every
// version — an unlisted version resolves to no published components — so tests
// never touch the network.
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
