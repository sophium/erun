package deploy

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// effectiveRuntimeChartCoordinateForImage lets the staleness check see the
// --runtime-chart override, which is applied to the spec only later.
func effectiveRuntimeChartCoordinateForImage(chart resolvedRuntimeChart, runtimeChartOverride string) (name, version string) {
	if override := strings.TrimSpace(runtimeChartOverride); override != "" {
		reference, overrideVersion := SplitChartReferenceVersion(override)
		return ChartName(reference), overrideVersion
	}
	return chart.name, chart.version
}

// resolveDeployRuntimeImage honors an image named on this invocation verbatim;
// a persisted pin still gets the staleness check. persistImage is the bare name
// to write back, empty for the erun product's own environments.
func resolveDeployRuntimeImage(ctx core.Context, target config.OpenResult, chartRegistry, version, chartName, chartVersion, runtimeChartOverride string, runtimeImageExplicit bool) (image, persistImage string) {
	chartName = strings.TrimSpace(chartName)
	version = strings.TrimSpace(version)
	registry := deployRuntimeImageRegistry(target, chartRegistry)
	tenant := strings.TrimSpace(target.Tenant)
	if image := resolveRuntimeImageOverride(registry, version, target.EnvConfig.RuntimeImage); image != "" {
		recorded := strings.TrimSpace(target.EnvConfig.RuntimeImage)
		if runtimeImageExplicit {
			ctx.Trace("deploy: runtime image override " + image + " (imageOverrides." + config.DevopsComponentName + ")")
			return image, recorded
		}
		staleChartName, staleChartVersion := effectiveRuntimeChartCoordinateForImage(resolvedRuntimeChart{name: chartName, version: chartVersion}, runtimeChartOverride)
		stale := staleRuntimeImageTrace(image, staleChartName, version, strings.TrimSpace(staleChartVersion))
		if stale == "" {
			if moved := laggingStockRuntimeImagePin(target, registry, image, staleChartName, version); moved != "" {
				ctx.Trace("deploy: moving the env's runtime image pin " + image + " to " + moved +
					" (the env rides " + config.DevopsComponentName + "'s own release line, so the pin moves with the deploy version)")
				return moved, moved
			}
			ctx.Trace("deploy: runtime image override " + image + " (imageOverrides." + config.DevopsComponentName + ")")
			return image, recorded
		}
		ctx.Trace(stale)
	}
	defaultImage := defaultDeployRuntimeImage(ctx, registry, version, tenant, chartName)
	return defaultImage, DefaultDeployRuntimeImageBareName(tenant, chartName)
}

// staleRuntimeImageTrace flags only a stock erun-devops pin on a line that never
// publishes it. A tag that differs from erun's version is expected on a tenant's
// own line, not evidence of drift.
func staleRuntimeImageTrace(image, chartName, version, chartVersion string) string {
	if !RuntimeImageIsStockDevops(image) {
		return ""
	}
	switch {
	case chartName != "" && chartName != config.DevopsComponentName:
		return "deploy: ignoring stale runtimeimage " + image + " on the " + chartName + " umbrella deploy (the stock " + config.DevopsComponentName + " image is not published on this version line); defaulting to the umbrella's own image"
	case chartVersion != "" && chartVersion != version:
		return "deploy: ignoring stale runtimeimage " + image + " (the env states its runtime chart at " + chartVersion + ", so version " + version + " is on another line and the stock " + config.DevopsComponentName + " image is not published at it); defaulting to the tenant's own image"
	}
	return ""
}

// stockRuntimePinMovesWithDeployVersion reports a stock chart/image pin left
// behind on erun's own line, where chart, image and runtime version are one
// coordinate. The line must be confirmed from the env's images, never guessed.
func stockRuntimePinMovesWithDeployVersion(tenant string, env config.EnvConfig, pinName, pinVersion, version string) bool {
	if strings.TrimSpace(pinName) != config.DevopsComponentName {
		return false
	}
	version = strings.TrimSpace(version)
	if strings.TrimSpace(pinVersion) == "" || strings.TrimSpace(pinVersion) == version || version == "" {
		return false
	}
	if config.RuntimeReleaseName(tenant) != config.DevopsComponentName {
		return false
	}
	for _, reference := range []string{env.RuntimeImage, env.RuntimeRunningImage} {
		if line, ok := config.RuntimeImageReleaseLine(reference); ok {
			return line == "erun"
		}
	}
	return false
}

// Takes the chart's resolved name so chart and image move together.
func laggingStockRuntimeImagePin(target config.OpenResult, registry, image, chartName, version string) string {
	if !RuntimeImageIsStockDevops(image) {
		return ""
	}
	_, tag, ok := SplitImageTag(image)
	if !ok {
		return ""
	}
	if !stockRuntimePinMovesWithDeployVersion(target.Tenant, target.EnvConfig, chartName, tag, version) {
		return ""
	}
	return moveRuntimeImagePinToVersion(image, registry, version)
}

func moveRuntimeImagePinToVersion(image, registry, version string) string {
	name := StripRuntimeImageTag(image)
	if name == "" {
		return ""
	}
	if !strings.Contains(name, "/") {
		name = strings.TrimSpace(registry) + "/" + name
	}
	return name + ":" + strings.TrimSpace(version)
}

// DefaultDeployRuntimeImageBareName returns "" for the erun product tenant, which
// rides the stock image and lets the chart default win.
func DefaultDeployRuntimeImageBareName(tenant, chartName string) string {
	if chartName != "" && chartName != config.DevopsComponentName {
		return chartName
	}
	tenantImage := config.RuntimeReleaseName(tenant)
	if tenant == "" || tenantImage == config.DevopsComponentName {
		return ""
	}
	return tenantImage
}

func defaultDeployRuntimeImageName(registry, version, tenant, chartName string) string {
	name := DefaultDeployRuntimeImageBareName(tenant, chartName)
	if name == "" {
		return ""
	}
	return registry + "/" + name + ":" + version
}

func defaultDeployRuntimeImage(ctx core.Context, registry, version, tenant, chartName string) string {
	image := defaultDeployRuntimeImageName(registry, version, tenant, chartName)
	if image == "" {
		return ""
	}
	if chartName != "" && chartName != config.DevopsComponentName {
		ctx.Trace("deploy: defaulting runtime image to the " + chartName + " chart's own image " + image + " (imageOverrides." + config.DevopsComponentName + ")")
	} else {
		ctx.Trace("deploy: defaulting runtime image to the tenant's " + config.RuntimeReleaseName(tenant) + " image " + image + " (imageOverrides." + config.DevopsComponentName + ")")
	}
	return image
}

// deployRuntimeImageRegistry prefers the DEPLOY registry: a stated chart points
// at erun's line while the env's own images stay where it publishes them.
func deployRuntimeImageRegistry(target config.OpenResult, chartRegistry string) string {
	if registry, ok := target.EnvConfig.ContainerRegistries.DeployRegistry(); ok {
		return registry
	}
	return strings.TrimSpace(chartRegistry)
}

// RuntimeImageIsStockDevops reports whether image names erun-devops in any
// registry.
func RuntimeImageIsStockDevops(image string) bool {
	_, name, _, ok := ParseDockerImageReference(image)
	return ok && name == config.DefaultRuntimeImageName
}

// A tagless reference would default to :latest, which is never published.
func resolveRuntimeImageOverride(registry, version, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if imageReferenceHasTagOrDigest(raw) {
		return raw
	}
	if !strings.Contains(raw, "/") {
		raw = strings.TrimSpace(registry) + "/" + raw
	}
	return raw + ":" + strings.TrimSpace(version)
}

// A ":" in the registry host is a port, so only the last segment is inspected.
func imageReferenceHasTagOrDigest(ref string) bool {
	if strings.Contains(ref, "@") {
		return true
	}
	lastSegment := ref[strings.LastIndex(ref, "/")+1:]
	return strings.Contains(lastSegment, ":")
}

// StripRuntimeImageTag drops a tag or digest so a persisted pin follows the env's
// runtime version instead of rotting.
func StripRuntimeImageTag(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if idx := strings.Index(ref, "@"); idx >= 0 {
		return ref[:idx]
	}
	prefix, repo := "", ref
	if lastSlash := strings.LastIndex(ref, "/"); lastSlash >= 0 {
		prefix, repo = ref[:lastSlash+1], ref[lastSlash+1:]
	}
	if idx := strings.LastIndex(repo, ":"); idx >= 0 {
		repo = repo[:idx]
	}
	return prefix + repo
}
