package runtimeview

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/helm"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartref"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// ResolveRuntimeChartPlan mirrors the deploy's resolution order so the tab and the deploy cannot drift.
func (s *Service) ResolveRuntimeChartPlan(tenant, environment, version string, runtimeIsLocalChart bool) uimodel.ChartPlan {
	version = strings.TrimSpace(version)
	if version == "" {
		return uimodel.ChartPlan{}
	}
	if runtimeIsLocalChart {
		return uimodel.ChartPlan{Source: "local", Version: version}
	}
	if plan, stated := s.statedRuntimeChartPlan(tenant, environment, version); stated {
		return plan
	}
	return s.probedRuntimeChartPlan(tenant, environment, version)
}

func (s *Service) statedRuntimeChartPlan(tenant, environment, version string) (uimodel.ChartPlan, bool) {
	env, _, err := s.d.Store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return uimodel.ChartPlan{}, false
	}
	stated := strings.TrimSpace(env.RuntimeChart)
	if stated == "" {
		return uimodel.ChartPlan{}, false
	}
	reference, statedVersion := chartref.SplitChartReference(stated)
	if statedVersion == "" {
		statedVersion = version
	}
	return uimodel.ChartPlan{
		Reference: reference,
		Version:   statedVersion,
		Chart:     chartref.ChartNameFromReference(reference),
		Source:    "stated",
	}, true
}

func (s *Service) probedRuntimeChartPlan(tenant, environment, version string) uimodel.ChartPlan {
	registry := strings.TrimSpace(s.d.RuntimeRegistryNamespace(tenant, environment))
	if registry == "" {
		registry = config.DefaultContainerRegistry
	}
	repo := func(chart string) string {
		return helm.PublishedDevopsChartOCIRepo(registry) + "/" + chart
	}

	unknown := false
	if tenantChart := config.RuntimeReleaseName(tenant); tenantChart != config.DevopsComponentName {
		published, known := s.probePublishedChart(tenant, environment, version, tenantChart)
		if published {
			return uimodel.ChartPlan{Reference: repo(tenantChart), Version: version, Chart: tenantChart, Source: "tenant"}
		}
		unknown = !known
	}

	canonical := config.DevopsComponentName
	plan := uimodel.ChartPlan{Reference: repo(canonical), Version: version, Chart: canonical, Source: "canonical"}
	published, known := s.probePublishedChart(tenant, environment, version, canonical)
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
func (s *Service) probePublishedChart(tenant, environment, version, chart string) (published, known bool) {
	if override, ok := s.d.ChartAvailabilityOverride(version); ok {
		return override[chart], true
	}
	ctx := s.d.Context()
	namespace := strings.TrimSpace(s.d.RuntimeRegistryNamespace(tenant, environment))
	if namespace == "" {
		namespace = config.DefaultContainerRegistry
	}
	versions, err := s.d.ResolveImageRegistry(ctx, namespace, "charts/"+chart)
	if err != nil {
		return false, false
	}
	return versions.HasVersion(version), true
}
