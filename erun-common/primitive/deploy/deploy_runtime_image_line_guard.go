package deploy

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/registry"
)

// guardRuntimeImageLineSwitch refuses, before any cluster mutation, a deploy that
// moves the pod between release lines (stock vs <tenant>-devops) without an
// explicit line change. The baseline is RuntimeRunningImage, not the possibly
// stale RuntimeImage; an unclassifiable side proceeds rather than blocks.
func guardRuntimeImageLineSwitch(ctx core.Context, target config.OpenResult, resolvedImage string, explicitLineChange bool) error {
	if explicitLineChange {
		return nil
	}
	resolvedLine, resolvedOK := config.RuntimeImageReleaseLine(resolvedImage)
	if !resolvedOK {
		return nil
	}
	previous := strings.TrimSpace(target.EnvConfig.RuntimeRunningImage)
	if previous == "" {
		return nil
	}
	previousLine, previousOK := config.RuntimeImageReleaseLine(previous)
	if !previousOK {
		ctx.Trace("deploy: runtime image line for " + previous + " (this env's last confirmed deploy) could not be classified; proceeding")
		return nil
	}
	if resolvedLine == previousLine {
		return nil
	}
	return fmt.Errorf("deploy: runtime image %s is on the %s release line, but %s/%s's last confirmed deploy ran %s (the %s line) -- pass --runtime-image or --runtime-chart to move release lines on purpose; if runtimeimage config just drifted, `erun doctor` explains how to realign it (erun#1754)",
		resolvedImage, resolvedLine, target.Tenant, target.Environment, previous, previousLine)
}

// guardRuntimeLineSwitch checks the image first, then a searched chart, which
// can be off-line while the tenant-derived image agrees with itself. A chart
// stated in config is the operator's coordinate and is not checked.
func guardRuntimeLineSwitch(ctx core.Context, target config.OpenResult, chart resolvedRuntimeChart, resolvedImage string, explicitLineChange bool) error {
	if err := guardRuntimeImageLineSwitch(ctx, target, resolvedImage, explicitLineChange); err != nil {
		return err
	}
	if !chart.searched {
		return nil
	}
	return guardRuntimeChartLineSwitch(ctx, target, chart.name, explicitLineChange)
}

// guardRuntimeChartLineSwitch catches the chart search falling through to the
// stock erun-devops chart at a version from erun's line on a tenant whose image
// line never published that tag: the Recreate rollout would sit in
// ImagePullBackOff until helm's timeout.
func guardRuntimeChartLineSwitch(ctx core.Context, target config.OpenResult, resolvedChart string, explicitLineChange bool) error {
	if explicitLineChange {
		return nil
	}
	resolvedLine, resolvedOK := config.RuntimeImageReleaseLine(resolvedChart)
	if !resolvedOK {
		return nil
	}
	previous := strings.TrimSpace(target.EnvConfig.RuntimeRunningImage)
	if previous == "" {
		return nil
	}
	previousLine, previousOK := config.RuntimeImageReleaseLine(previous)
	if !previousOK {
		// guardRuntimeImageLineSwitch already traced this baseline.
		return nil
	}
	if resolvedLine == previousLine {
		return nil
	}
	return fmt.Errorf("deploy: %s/%s's last confirmed deploy ran %s, so this environment is on the %s release line, but this deploy resolved the %s chart, which is versioned on erun's own release line -- a version from erun's line does not name anything %s publishes, so the pod would wait on an image tag that does not exist; pass --version from %s's line, or --runtime-chart to install this chart on purpose",
		target.Tenant, target.Environment, previous, previousLine, resolvedChart, previousLine, previousLine)
}

// guardRuntimeImageNotOlderThanChart refuses a stock image older than the stock
// chart, whose init containers and dind sidecar run the image's own erun. It
// applies even to an explicit image; tenant images are not checked.
func guardRuntimeImageNotOlderThanChart(image, chartName, chartVersion string) error {
	if strings.TrimSpace(chartName) != config.DevopsComponentName || !RuntimeImageIsStockDevops(image) {
		return nil
	}
	_, _, tag, ok := ParseDockerImageReference(strings.TrimSpace(image))
	if !ok {
		return nil
	}
	imageRelease, imageOK := runtimeReleaseBase(tag)
	chartRelease, chartOK := runtimeReleaseBase(chartVersion)
	if !imageOK || !chartOK || registry.CompareSemver(imageRelease, chartRelease) >= 0 {
		return nil
	}
	return fmt.Errorf("deploy: runtime image %s is from erun %s, older than the %s %s chart this deploy installs; the chart runs its init containers and dind sidecar on the image's own erun, so the rollout would fail after the running pod is torn down -- deploy an image at %s or newer with --runtime-image, or the chart matching the image with --runtime-chart %s:%s",
		image, registry.FormatSemver(imageRelease), config.DevopsComponentName, strings.TrimSpace(chartVersion), registry.FormatSemver(chartRelease), config.DevopsComponentName, tag)
}

// runtimeReleaseBase maps 1.0.307, 1.0.307-rc.1, 1.0.307-snapshot-<ts>-arm64 to
// 1.0.307; fingerprint tags and digests do not classify.
func runtimeReleaseBase(version string) (registry.Semver, bool) {
	base, _, _ := strings.Cut(strings.TrimSpace(version), "-")
	return registry.ParseRegistryStableVersion(base)
}

func guardRuntimeCoordinate(ctx core.Context, target config.OpenResult, chart resolvedRuntimeChart, runtimeChartOverride string, deployInput seam.HelmDeploySpec, explicitLineChange bool) error {
	if err := guardRuntimeLineSwitch(ctx, target, chart, deployInput.ResolvedRuntimeImage, explicitLineChange); err != nil {
		return err
	}
	name, version := effectiveRuntimeChartCoordinateForImage(chart, runtimeChartOverride)
	if strings.TrimSpace(version) == "" {
		version = resolvedChartVersion(deployInput)
	}
	return guardRuntimeImageNotOlderThanChart(deployInput.ResolvedRuntimeImage, name, version)
}
