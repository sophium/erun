package deploy

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// effectiveRuntimeChartCoordinateForImage answers which chart name/version a
// persisted runtimeimage's staleness check should read: the operator's
// --runtime-chart when one is coming, never the coordinate
// resolveRuntimeChartCoordinate found before that override is applied.
// applyRuntimeChartOverride (deploy.go) still applies the override to the
// deploy spec itself, later in the same resolve; this lets the staleness
// check — computed here, earlier — see the coordinate the deploy will
// actually install rather than the one about to be replaced. Reading the
// pre-override coordinate for this decision was the ordering bug behind
// #1249: an env whose recorded runtimechart named an old version made every
// staleness decision as if that stale version were still current, even on a
// deploy whose own --runtime-chart said otherwise. Scoped to the staleness
// check alone: the plain default-image fallback (no runtimeimage at all) has
// no override to protect and must keep naming the image after the chart as
// actually resolved, not the one about to replace it.
func effectiveRuntimeChartCoordinateForImage(chart resolvedRuntimeChart, runtimeChartOverride string) (name, version string) {
	if override := strings.TrimSpace(runtimeChartOverride); override != "" {
		reference, overrideVersion := SplitChartReferenceVersion(override)
		return ChartName(reference), overrideVersion
	}
	return chart.name, chart.version
}

// resolveDeployRuntimeImage resolves the image the runtime pod's erun-devops
// container runs, as the imageOverrides.erun-devops the deploy sets. An image
// the operator named on this very invocation (runtimeImageExplicit) is used
// verbatim, full stop — an inference this deploy cannot confirm must never
// override what the operator explicitly stated (#1249). A runtimeimage that is
// merely a persisted memo from a previous deploy still gets the staleness
// check below -- keyed off the operator's --runtime-chart when one is coming,
// never the coordinate resolveRuntimeChartCoordinate found before that override
// is applied to the deploy spec (the same #1249 ordering fix) -- so an old pin
// left over from riding a different chart/version line can still be healed
// automatically. Otherwise the deploy's own line names the image, keyed off
// chartName/chartVersion exactly as resolved: the operator stated no image at
// all here, so there is nothing to protect from the override.
//
// persistImage is what PersistRuntimeVersionFromDeploySpecs should write back
// to EnvConfig.RuntimeImage after the deploy succeeds -- the bare component
// name only (never a registry or tag), so the pin stays self-maintaining the
// same way an operator's own `erun init --runtime-image` pin does
// (stripRuntimeImageTag). When the persisted override is honored verbatim
// (explicit or not stale), nothing changed, so persistImage names the same
// image the config already records -- a no-op write. When it falls through to
// the deploy's own default, persistImage is the name that default resolved
// to, healing exactly the field a prior report found silently left behind. Empty
// only for the erun product's own environments, which have no line of their
// own to persist a name for.
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

// staleRuntimeImageTrace explains why a saved runtimeimage override cannot be
// honored on this deploy, or "" when it can be. A runtimeimage is stale only
// when it is provably wrong for the line this deploy is on: it names the
// stock erun-devops image on a deploy that is not on erun's own release line
// (a tenant umbrella, which publishes its own image beside its own chart, or
// a chart stated at its own version, which is the operator saying so
// outright). Neither line publishes the stock image at its version, so a
// runtimeimage still naming it is a leftover from when the env rode the
// shared chart, and honoring it would pin a tag that never existed
// (ImagePullBackOff).
//
// A recorded image naming a *different tag* than the version this deploy
// would otherwise guess is never treated as stale on that basis alone: a
// tenant's own image is versioned on the tenant's own release line, so a tag
// that disagrees with erun's version is the expected, correct case, not
// evidence of drift. Guessing that it names "an older tag" and silently
// substituting the erun-version-tagged guess pins a tag the tenant's registry
// may never have published at all.
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

// stockRuntimePinMovesWithDeployVersion reports whether a stated stock
// erun-devops runtime coordinate — a chart's explicit version, or an image
// pin's tag — is a lagging pin this deploy's version moves, rather than the
// operator's own coordinate on another line.
//
// erun publishes the stock erun-devops image and chart together on erun's own
// release line, so for an environment riding that line the two numbers are one
// coordinate with the recorded runtime version. A deploy version that has moved
// on then makes the stated one a pin left behind by an earlier deploy, and
// honoring it installs the older chart and image while the deploy still records
// the newer version — a version the pods are not running.
//
// Two things must hold, and neither is inferred from the tenant name alone:
//
//   - The environment's runtime coordinates must be confirmed on erun's line.
//     EnvConfig.RuntimeImage is read first, the operative pin, then
//     RuntimeRunningImage, the last image a deploy actually confirmed; a
//     reference this cannot classify leaves the pin alone, the same "never
//     guess a line" rule RuntimeVersionLine follows.
//   - The deploy's own version must be able to be on that line at all. A tenant
//     that publishes a devops image of its own runs its components on its own
//     version line — which is exactly why it states its runtime chart
//     separately — so a stated *stock* chart there is a deliberate coordinate on
//     erun's line, not this deploy's, and must not move. That exemption is about
//     which line the stated chart is on, not about the tenant: a tenant that
//     states its *own* <tenant>-devops umbrella has named a chart on the
//     deploy's own line, which resolveTenantUmbrellaPin handles.
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

// laggingStockRuntimeImagePin re-pins a stock runtime image the deploy is about
// to honor at the deploy version, or returns "" when the pin is not the lagging
// same-line one stockRuntimePinMovesWithDeployVersion describes. chartName is
// the chart coordinate the same deploy resolved, so the two halves of the
// coordinate move together or not at all.
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

// moveRuntimeImagePinToVersion restates an image reference at version, keeping
// the registry and name it was stated with and qualifying a bare name with the
// registry the deploy resolves runtime images from — the same shape
// resolveRuntimeImageOverride gives a tagless pin.
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

// DefaultDeployRuntimeImageBareName names the bare (no registry, no tag)
// image the deploy's own line publishes: the umbrella's own name, when the
// tenant deploys its own <tenant>-devops chart, else the tenant's own
// <tenant>-devops name, which erun-build-env builds and erun push publishes
// on the tenant's version line. Empty means the deploy has no line of its own
// to default to (the erun product tenant rides the stock image itself, so it
// emits no override and the chart's own default wins).
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

// defaultDeployRuntimeImageName names the full, tagged image reference for
// defaultDeployRuntimeImageBareName. Empty means the deploy has no line of
// its own to default to -- see that function.
func defaultDeployRuntimeImageName(registry, version, tenant, chartName string) string {
	name := DefaultDeployRuntimeImageBareName(tenant, chartName)
	if name == "" {
		return ""
	}
	return registry + "/" + name + ":" + version
}

// defaultDeployRuntimeImage resolves defaultDeployRuntimeImageName and traces
// the decision. See that function for the resolution itself.
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

// deployRuntimeImageRegistry answers which registry the runtime image is pulled
// from. The chart's registry is not it: an env that states its runtime chart
// points the chart at erun's line on purpose, while its own images stay where it
// builds and publishes them. So the image follows the role that describes the
// cluster's pull, and falls back to the chart's registry only when no entry
// marks one.
func deployRuntimeImageRegistry(target config.OpenResult, chartRegistry string) string {
	if registry, ok := target.EnvConfig.ContainerRegistries.DeployRegistry(); ok {
		return registry
	}
	return strings.TrimSpace(chartRegistry)
}

// RuntimeImageIsStockDevops reports whether a resolved runtime image reference
// names the stock erun-devops image (regardless of registry). A tenant umbrella
// deploy uses it to detect a stale runtimeimage pin left over from the shared
// erun-devops chart, which its own version line never publishes.
func RuntimeImageIsStockDevops(image string) bool {
	_, name, _, ok := shared.ParseDockerImageReference(image)
	return ok && name == config.DefaultRuntimeImageName
}

// resolveRuntimeImageOverride normalizes a custom runtime image. A reference
// that already pins a tag or digest is used verbatim; a tagless one is pinned
// to the env's runtime version, because a bare registry path would otherwise
// default to :latest — a tag the release flow never publishes (ImagePullBackOff).
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

// imageReferenceHasTagOrDigest reports whether an image reference already pins a
// tag or digest. A ":" in the registry host is a port, not a tag (e.g.
// localhost:5000/img), so only the segment after the last "/" is inspected.
func imageReferenceHasTagOrDigest(ref string) bool {
	if strings.Contains(ref, "@") {
		return true
	}
	lastSegment := ref[strings.LastIndex(ref, "/")+1:]
	return strings.Contains(lastSegment, ":")
}

// StripRuntimeImageTag drops a trailing tag or digest from an operator-supplied
// runtime image reference, e.g. for `erun init --runtime-image` to persist. A
// tagged reference sticks at that tag forever: resolveRuntimeImageOverride
// already pins a tagless reference to the env's own runtime version on every
// deploy, so recording the tag is what lets the pin drift from that version and
// rot, while recording it tagless is self-maintaining.
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
