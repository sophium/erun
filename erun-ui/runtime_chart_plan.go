package main

import (
	"context"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// uiRuntimeChartPlan is what a deploy at the chosen version would install for the
// runtime, resolved before the operator commits to it.
//
// A deploy names four things -- chart repository, chart version, image repository,
// image version -- and the version picker names only the image's. For an
// environment whose image rides its project's release line, the chart is a
// separate artifact on ERun's line, and there is no chart at the picked version at
// all. That used to surface as a failed rollout; this plan is what lets the
// Runtime tab say so first, and name the fix.
type uiRuntimeChartPlan struct {
	// Reference is the chart repository a deploy would install from, and Version
	// the version it would be pulled at.
	Reference string `json:"reference"`
	Version   string `json:"version"`
	// Chart is the chart's own name, for a label that reads in the operator's
	// terms rather than as a URL.
	Chart string `json:"chart"`
	// Source names where this came from: "stated" (the environment's runtimechart
	// -- the operator said so), "tenant" (the tenant's own umbrella, published at
	// this version), "canonical" (the shared erun-devops chart) or "local" (a
	// repo-local chart in the worktree, which no registry decides).
	Source string `json:"source"`
	// Missing is set only when the registry positively answered that no chart
	// exists at this version -- the deploy would fail, and the operator can be
	// told before starting it. An unreachable or private registry leaves this
	// false and sets Unknown, because a guess must never block a deploy that would
	// have worked.
	Missing bool `json:"missing"`
	Unknown bool `json:"unknown"`
}

// resolveRuntimeChartPlan answers which chart the runtime would be installed from
// at this version. It mirrors the deploy's own resolution order -- the
// environment's stated chart, else the tenant's published umbrella, else the
// canonical chart -- so what the tab reports and what the deploy does cannot
// drift.
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

// statedRuntimeChartPlan is the environment's own answer, when it has one. An env
// that states its chart is taken at its word -- no registry is asked, because the
// operator already decided.
func (a *App) statedRuntimeChartPlan(tenant, environment, version string) (uiRuntimeChartPlan, bool) {
	env, _, err := a.deps.store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return uiRuntimeChartPlan{}, false
	}
	stated := strings.TrimSpace(env.RuntimeChart)
	if stated == "" {
		return uiRuntimeChartPlan{}, false
	}
	reference, statedVersion := eruncommon.SplitChartReference(stated)
	if statedVersion == "" {
		statedVersion = version
	}
	return uiRuntimeChartPlan{
		Reference: reference,
		Version:   statedVersion,
		Chart:     eruncommon.ChartNameFromReference(reference),
		Source:    "stated",
	}, true
}

// probedRuntimeChartPlan asks the registry the same question the deploy asks: the
// tenant's own umbrella at this version, else the canonical chart. Both answering
// "not published" is the definite miss worth blocking on; either failing to answer
// leaves it unknown.
func (a *App) probedRuntimeChartPlan(tenant, environment, version string) uiRuntimeChartPlan {
	registry := strings.TrimSpace(a.runtimeRegistryNamespace(tenant, environment))
	if registry == "" {
		registry = eruncommon.DefaultContainerRegistry
	}
	repo := func(chart string) string {
		return eruncommon.PublishedDevopsChartOCIRepo(registry) + "/" + chart
	}

	unknown := false
	if tenantChart := eruncommon.RuntimeReleaseName(tenant); tenantChart != eruncommon.DevopsComponentName {
		published, known := a.probePublishedChart(tenant, environment, version, tenantChart)
		if published {
			return uiRuntimeChartPlan{Reference: repo(tenantChart), Version: version, Chart: tenantChart, Source: "tenant"}
		}
		unknown = !known
	}

	canonical := eruncommon.DevopsComponentName
	plan := uiRuntimeChartPlan{Reference: repo(canonical), Version: version, Chart: canonical, Source: "canonical"}
	published, known := a.probePublishedChart(tenant, environment, version, canonical)
	switch {
	case published:
		return plan
	case !known || unknown:
		plan.Unknown = true
	default:
		// Both answers came back, and neither chart exists at this version: the
		// version names an image and nothing else.
		plan.Missing = true
	}
	return plan
}

// probePublishedChart reports whether charts/<chart> is published at the version,
// and whether the registry actually answered. The two are distinct: a private or
// unreachable registry means "not known", never "not published", so a listing
// failure cannot be mistaken for a missing chart.
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
		namespace = eruncommon.DefaultContainerRegistry
	}
	versions, err := a.deps.resolveImageRegistry(ctx, namespace, "charts/"+chart)
	if err != nil {
		return false, false
	}
	return versions.HasVersion(version), true
}
