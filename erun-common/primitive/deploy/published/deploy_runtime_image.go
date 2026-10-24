package published

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartref"
	"github.com/sophium/erun/erun-common/primitive/deploy/runtimeimage"
)

// effectiveRuntimeChartCoordinateForImage lets the staleness check see the
// --runtime-chart override, which is applied to the spec only later.
func effectiveRuntimeChartCoordinateForImage(chart resolvedRuntimeChart, runtimeChartOverride string) (name, version string) {
	if override := strings.TrimSpace(runtimeChartOverride); override != "" {
		reference, overrideVersion := chartref.SplitChartReferenceVersion(override)
		return chartref.ChartName(reference), overrideVersion
	}
	return chart.name, chart.version
}

// resolveDeployRuntimeImage honors an image named on this invocation verbatim;
// a persisted pin still gets the staleness check. persistImage is the bare name
// to write back, empty for the erun product's own environments.
func resolveDeployRuntimeImage(ctx core.Context, target config.OpenResult, chartRegistry, version, chartName, chartVersion, runtimeChartOverride string, runtimeImageExplicit bool) (image, persistImage string) {
	chartName = strings.TrimSpace(chartName)
	version = strings.TrimSpace(version)
	registry := runtimeimage.DeployRuntimeImageRegistry(target, chartRegistry)
	tenant := strings.TrimSpace(target.Tenant)
	if image := runtimeimage.ResolveRuntimeImageOverride(registry, version, target.EnvConfig.RuntimeImage); image != "" {
		recorded := strings.TrimSpace(target.EnvConfig.RuntimeImage)
		if runtimeImageExplicit {
			ctx.Trace("deploy: runtime image override " + image + " (imageOverrides." + config.DevopsComponentName + ")")
			return image, recorded
		}
		staleChartName, staleChartVersion := effectiveRuntimeChartCoordinateForImage(resolvedRuntimeChart{name: chartName, version: chartVersion}, runtimeChartOverride)
		stale := runtimeimage.StaleRuntimeImageTrace(image, staleChartName, version, strings.TrimSpace(staleChartVersion))
		if stale == "" {
			if moved := runtimeimage.LaggingStockRuntimeImagePin(target, registry, image, staleChartName, version); moved != "" {
				ctx.Trace("deploy: moving the env's runtime image pin " + image + " to " + moved +
					" (the env rides " + config.DevopsComponentName + "'s own release line, so the pin moves with the deploy version)")
				return moved, moved
			}
			ctx.Trace("deploy: runtime image override " + image + " (imageOverrides." + config.DevopsComponentName + ")")
			return image, recorded
		}
		ctx.Trace(stale)
	}
	defaultImage := runtimeimage.DefaultDeployRuntimeImage(ctx, registry, version, tenant, chartName)
	return defaultImage, runtimeimage.DefaultDeployRuntimeImageBareName(tenant, chartName)
}
