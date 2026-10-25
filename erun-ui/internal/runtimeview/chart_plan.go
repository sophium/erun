package runtimeview

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/helm"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartref"
)

// ChartPlan is the runtime chart a deploy at the chosen version would
// install; the picked version may name an image with no chart at that version.
type ChartPlan struct {
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

// ResolveRuntimeChartPlan mirrors the deploy's resolution order so the tab and the deploy cannot drift.
func (s *Service) ResolveRuntimeChartPlan(tenant, environment, version string, runtimeIsLocalChart bool) ChartPlan {
	version = strings.TrimSpace(version)
	if version == "" {
		return ChartPlan{}
	}
	if runtimeIsLocalChart {
		return ChartPlan{Source: "local", Version: version}
	}
	if plan, stated := s.statedRuntimeChartPlan(tenant, environment, version); stated {
		return plan
	}
	return s.probedRuntimeChartPlan(tenant, environment, version)
}

func (s *Service) statedRuntimeChartPlan(tenant, environment, version string) (ChartPlan, bool) {
	env, _, err := s.d.Store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return ChartPlan{}, false
	}
	stated := strings.TrimSpace(env.RuntimeChart)
	if stated == "" {
		return ChartPlan{}, false
	}
	reference, statedVersion := chartref.SplitChartReference(stated)
	if statedVersion == "" {
		statedVersion = version
	}
	return ChartPlan{
		Reference: reference,
		Version:   statedVersion,
		Chart:     chartref.ChartNameFromReference(reference),
		Source:    "stated",
	}, true
}

func (s *Service) probedRuntimeChartPlan(tenant, environment, version string) ChartPlan {
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
			return ChartPlan{Reference: repo(tenantChart), Version: version, Chart: tenantChart, Source: "tenant"}
		}
		unknown = !known
	}

	canonical := config.DevopsComponentName
	plan := ChartPlan{Reference: repo(canonical), Version: version, Chart: canonical, Source: "canonical"}
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
