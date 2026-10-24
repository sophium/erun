package eruncommon

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/registry"
)

// guardRuntimeImageLineSwitch refuses a runtime deploy that would move the pod
// from one release line to a different one -- stock erun-devops vs a
// tenant's own <tenant>-devops -- without the operator saying so on this very
// call. It exists for the pairing erun#1754 was filed over: a persisted
// runtimeimage can drift away from the line an environment actually moved
// onto (nothing catches half of a two-field coordinate updating), and the
// wrong tag can resolve fine -- erun's own stock image genuinely exists at
// almost any version number a tenant's own line also uses -- so a deploy that
// only checks tag existence installs it instead of refusing. The runtime
// chart's Recreate strategy tears the running pod down before the
// replacement is scheduled, so this runs at spec-resolution time, before any
// cluster mutation.
//
// The baseline is EnvConfig.RuntimeRunningImage, the last image a deploy
// actually confirmed running (healed alongside RuntimeVersion by
// PersistRuntimeVersionFromDeploySpecs) -- not the operative RuntimeImage
// field, which is exactly the value this issue found stale. Comparing against
// the observed truth rather than the (possibly stale) config catches the
// dangerous case directly: a deploy about to move the pod off the line it is
// actually running, with nothing on this call saying that is intended.
//
// Three outcomes:
//   - explicitLineChange (an operator's own --runtime-image/--runtime-chart on
//     this call, or a build --deploy of the working tree's own image) always
//     proceeds: moving release lines on purpose is exactly what those inputs
//     are for.
//   - Both sides classify and disagree: refuse before rollout.
//   - Either side does not classify (no prior deploy recorded yet, or an
//     image reference this guard cannot parse into a component name) never
//     blocks -- an unclassifiable pairing must not read as fine, but it must
//     not read as wrong either (root AGENTS.md: "never block a legitimate
//     configuration you merely could not classify"), so it proceeds with a
//     trace instead of a refusal.
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

// guardRuntimeLineSwitch applies the line guard to both halves of the runtime
// coordinate a deploy is about to install: the image, always, and the chart
// when the chart search produced it rather than the operator.
//
// The image half runs first, so a deploy that disagrees on both halves is still
// reported by the image it would install -- the pairing guardRuntimeImageLineSwitch
// already described, and the message its own callers already read. The chart
// half covers the disagreement the image half structurally cannot see: the image
// is derived from the tenant, so it agrees with itself while only the chart is
// off the environment's line. It does not apply to a chart stated in the env's
// own config (resolvedRuntimeChart.searched): that is the operator's coordinate,
// and a tenant deliberately stating the stock erun-devops chart on erun's line
// while running its own image line must keep deploying as stated.
func guardRuntimeLineSwitch(ctx core.Context, target config.OpenResult, chart resolvedRuntimeChart, resolvedImage string, explicitLineChange bool) error {
	if err := guardRuntimeImageLineSwitch(ctx, target, resolvedImage, explicitLineChange); err != nil {
		return err
	}
	if !chart.searched {
		return nil
	}
	return guardRuntimeChartLineSwitch(ctx, target, chart.name, explicitLineChange)
}

// guardRuntimeChartLineSwitch is the chart half of the same coordinate
// guardRuntimeImageLineSwitch protects, and refuses the case that half leaves
// open: the runtime image resolves on the environment's OWN release line while
// the runtime CHART resolves on a different one. Nothing catches that, because
// the runtime image's own component name is what the deploy derives from the
// tenant, so both halves of the image coordinate agree with each other and
// only the chart is off its line.
//
// It exists for the reported move: `erun deploy frs build
// --version 1.0.304` on an environment whose product is frs. frs publishes
// frs-devops on its own release line, but the chart search falls through to
// the shared erun-devops chart when the tenant's umbrella is not published at
// the requested version -- and erun-devops IS published at 1.0.304, because
// that number is an erun release. The deploy then installs an erun chart at an
// erun version onto frs's release while defaulting the image to
// frs-devops:1.0.304, a tag frs's line never published: the pod sits in
// Init:ImagePullBackOff, the previous pod is already gone (every runtime chart
// renders strategy: Recreate), and the only thing that eventually surfaces is
// helm's rollout timeout -- ten minutes of downtime naming anything but the
// cause. A released number from the wrong product is a reachable, repeated
// mistake, not a typo: the same release's history carries an earlier failed
// revision with an erun chart on an otherwise all-frs-devops line.
//
// The baseline is the same one guardRuntimeImageLineSwitch uses and for the
// same reason -- EnvConfig.RuntimeRunningImage, the image the environment's
// last confirmed deploy actually ran, never the tenant name by convention. An
// environment that has never had a deploy recorded is undetermined, and
// undetermined proceeds rather than refusing: a chart this guard merely could
// not classify must not block a legitimate configuration (root AGENTS.md). The
// erun product's own environments resolve the stock chart directly, so chart
// line and running line are the same and nothing changes for them.
//
// Callers apply this to a coordinate the chart search produced, never to one
// the env states outright (resolvedRuntimeChart.searched). A tenant that
// deliberately states the stock erun-devops chart in its config while running
// its own image line is naming erun's line on purpose, and its stated version
// is the operator's coordinate that this deploy must install as given --
// stockRuntimePinMovesWithDeployVersion's decision, and the legitimate
// configuration this guard must not turn into a refusal.
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
		// Proceed without a trace of its own: guardRuntimeImageLineSwitch reads
		// the same field, and an observed baseline it could not classify is one
		// fact about one value -- reporting it again here would say the same
		// thing twice in the same trace.
		return nil
	}
	if resolvedLine == previousLine {
		return nil
	}
	return fmt.Errorf("deploy: %s/%s's last confirmed deploy ran %s, so this environment is on the %s release line, but this deploy resolved the %s chart, which is versioned on erun's own release line -- a version from erun's line does not name anything %s publishes, so the pod would wait on an image tag that does not exist; pass --version from %s's line, or --runtime-chart to install this chart on purpose",
		target.Tenant, target.Environment, previous, previousLine, resolvedChart, previousLine, previousLine)
}

// guardRuntimeImageNotOlderThanChart refuses a deploy that would run the stock
// erun-devops chart on a stock erun-devops image from an earlier erun release.
// The chart's init containers and dind sidecar run the image's own erun
// (`erun adopt-worktree`, and `erun dind-entrypoint` copied out of the image),
// so the image must be at least as new as the chart: the two are one coordinate
// on erun's release line. An older image fails the rollout at init or
// crash-loops the sidecar, and the chart's Recreate strategy has already torn
// the running pod down by then, so this runs before any cluster mutation. It
// applies even to an image the operator named on this call, because no input
// makes that pairing run. A tenant's own image is versioned on its own line, so
// its erun base cannot be read off its tag and it is not checked here: the
// erun-build-env contract re-pins its base with the chart.
func guardRuntimeImageNotOlderThanChart(image, chartName, chartVersion string) error {
	if strings.TrimSpace(chartName) != config.DevopsComponentName || !runtimeImageIsStockDevops(image) {
		return nil
	}
	_, _, tag, ok := parseDockerImageReference(strings.TrimSpace(image))
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

// runtimeReleaseBase reads the release a version or image tag belongs to:
// 1.0.307, 1.0.307-snapshot, 1.0.307-rc.1 and 1.0.307-snapshot-<ts>-arm64 are
// all 1.0.307. Anything else (a fingerprint tag, a digest) does not classify.
func runtimeReleaseBase(version string) (registry.Semver, bool) {
	base, _, _ := strings.Cut(strings.TrimSpace(version), "-")
	return registry.ParseRegistryStableVersion(base)
}

// guardRuntimeCoordinate runs the runtime coordinate's pre-mutation guards: the
// release-line switch, then the image against the chart this deploy will
// actually install (the operator's --runtime-chart when one is coming,
// otherwise the resolved coordinate).
func guardRuntimeCoordinate(ctx core.Context, target config.OpenResult, chart resolvedRuntimeChart, runtimeChartOverride string, deployInput HelmDeploySpec, explicitLineChange bool) error {
	if err := guardRuntimeLineSwitch(ctx, target, chart, deployInput.ResolvedRuntimeImage, explicitLineChange); err != nil {
		return err
	}
	name, version := effectiveRuntimeChartCoordinateForImage(chart, runtimeChartOverride)
	if strings.TrimSpace(version) == "" {
		version = deployInput.resolvedChartVersion()
	}
	return guardRuntimeImageNotOlderThanChart(deployInput.ResolvedRuntimeImage, name, version)
}
