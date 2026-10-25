package spec

import (
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartref"
	"github.com/sophium/erun/erun-common/primitive/deploy/published"
)

type deployKubernetesContextResolver interface {
	ResolveDeployKubernetesContext(environment, configured string) string
}

// applyRuntimeChartOverride installs the operator-named runtime chart. erun publishes the chart on
// its own line, so a tenant-versioned image may have no chart at its version.
func applyRuntimeChartOverride(ctx core.Context, target model.DeployTarget, spec *helmdeploy.DeploySpec) {
	override := strings.TrimSpace(target.RuntimeChartOverride)
	if override == "" || spec == nil {
		return
	}
	if spec.Deploy.ReleaseName != configpkg.RuntimeReleaseName(spec.Target.Tenant) {
		return
	}
	reference, version := chartref.SplitChartReferenceVersion(override)
	spec.DeployContext.ChartPath = reference
	spec.Deploy.ChartPath = reference
	spec.Deploy.ChartVersion = version
	spec.Deploy.PersistRuntimeChart = override
	// The subchart key belongs to the named chart: helm silently ignores values for a missing subchart,
	// so a stale key would drop the image override and required values.
	spec.Deploy.SubchartKey = published.PublishedUmbrellaSubchartKey(spec.Target.Tenant, chartref.ChartName(reference))
	if version == "" {
		ctx.Trace("deploy: runtime chart override " + reference + " at the deploy version")
		return
	}
	ctx.Trace("deploy: runtime chart override " + reference + " version " + version)
}
