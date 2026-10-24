package main

import (
	"context"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/helm"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartref"
)

// uiRuntimeChartPlan is the runtime chart a deploy at the chosen version would
// install; the picked version may name an image with no chart at that version.
type uiRuntimeChartPlan struct {
	Reference string `json:"reference"`
	Version   string `json:"version"`
	Chart     string `json:"chart"`
	// Source is "stated", "tenant", "canonical" or "local".
	Source string `json:"source"`
	// Missing means the registry positively answered "no chart"; an unreadable
	// registry sets Unknown instead, so a guess never blocks a deploy.
	Missing bool `json:"missing"`
	Unknown bool `json:"unknown"`
}

// resolveRuntimeChartPlan mirrors the deploy's resolution order so the tab and the deploy cannot drift.
func (a *App) resolveRuntimeChartPlan(tenant, environment, version string, runtimeIsLocalChart bool) uiRuntimeChartPlan {
	version = strings.TrimSpace(version)
	if version == "" {
		return uiRuntimeChartPlan{}
	}
	if runtimeIsLocalChart {
		return uiRuntimeChartPlan{Source: "local", Version: version}
	}
	if plan, stated := a.statedRuntimeChartPlan(tenant, environment, version); stated {
		return plan
	}
	return a.probedRuntimeChartPlan(tenant, environment, version)
}

func (a *App) statedRuntimeChartPlan(tenant, environment, version string) (uiRuntimeChartPlan, bool) {
	env, _, err := a.deps.store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return uiRuntimeChartPlan{}, false
	}
	stated := strings.TrimSpace(env.RuntimeChart)
	if stated == "" {
		return uiRuntimeChartPlan{}, false
	}
	reference, statedVersion := chartref.SplitChartReference(stated)
	if statedVersion == "" {
		statedVersion = version
	}
	return uiRuntimeChartPlan{
		Reference: reference,
		Version:   statedVersion,
		Chart:     chartref.ChartNameFromReference(reference),
		Source:    "stated",
	}, true
}

func (a *App) probedRuntimeChartPlan(tenant, environment, version string) uiRuntimeChartPlan {
	registry := strings.TrimSpace(a.runtimeRegistryNamespace(tenant, environment))
	if registry == "" {
		registry = config.DefaultContainerRegistry
	}
	repo := func(chart string) string {
		return helm.PublishedDevopsChartOCIRepo(registry) + "/" + chart
	}

	unknown := false
	if tenantChart := config.RuntimeReleaseName(tenant); tenantChart != config.DevopsComponentName {
		published, known := a.probePublishedChart(tenant, environment, version, tenantChart)
		if published {
			return uiRuntimeChartPlan{Reference: repo(tenantChart), Version: version, Chart: tenantChart, Source: "tenant"}
		}
		unknown = !known
	}

	canonical := config.DevopsComponentName
	plan := uiRuntimeChartPlan{Reference: repo(canonical), Version: version, Chart: canonical, Source: "canonical"}
	published, known := a.probePublishedChart(tenant, environment, version, canonical)
	switch {
	case published:
		return plan
	case !known || unknown:
		plan.Unknown = true
	default:
		plan.Missing = true
	}
	return plan
}

// probePublishedChart: known=false means the registry did not answer, never "not published".
func (a *App) probePublishedChart(tenant, environment, version, chart string) (published, known bool) {
	if override, ok := chartAvailabilityOverride(version); ok {
		return override[chart], true
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	namespace := strings.TrimSpace(a.runtimeRegistryNamespace(tenant, environment))
	if namespace == "" {
		namespace = config.DefaultContainerRegistry
	}
	versions, err := a.deps.resolveImageRegistry(ctx, namespace, "charts/"+chart)
	if err != nil {
		return false, false
	}
	return versions.HasVersion(version), true
}
