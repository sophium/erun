package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PublishedDevopsChartOCIRepo is the OCI repository the release flow pushes
// the canonical runtime chart to. The "/charts" suffix keeps the chart's tag
// space separate from the image repository of the same name
// (<registry>/erun-devops), which holds the runtime image tags.
func PublishedDevopsChartOCIRepo(containerRegistry string) string {
	return "oci://" + strings.TrimSpace(containerRegistry) + "/charts"
}

func publishedDevopsChartReference(containerRegistry string) string {
	return PublishedDevopsChartOCIRepo(containerRegistry) + "/" + DevopsComponentName
}

// IsOCIChartReference reports whether the chart path addresses a published
// OCI chart (oci://<registry>/charts/<name>) rather than a local chart
// directory. Transports use it to tell a published-chart runtime spec from a
// repo-local one.
func IsOCIChartReference(chartPath string) bool {
	return isOCIChartReference(chartPath)
}

// ResolvePublishedDevopsDeploySpec rebuilds the runtime deploy spec against
// the published chart for transport flows that override inputs after the
// initial resolution (e.g. `erun open --runtime-image`).
func ResolvePublishedDevopsDeploySpec(ctx Context, target OpenResult, versionOverride string) (DeploySpec, error) {
	return resolvePublishedDevopsDeploySpec(ctx, target, versionOverride)
}

// resolvePublishedDevopsDeploySpec builds the deploy spec for an environment
// with no local runtime chart, using the published erun-devops chart pinned to
// the env's runtime version (one version covers both chart and image, published
// together at release). The published chart is the single contract, replacing
// the per-tenant embedded chart copy init once scaffolded, which had drifted
// from canonical.
func resolvePublishedDevopsDeploySpec(ctx Context, target OpenResult, versionOverride string) (DeploySpec, error) {
	return resolvePublishedDevopsDeploySpecWithReason(ctx, target, versionOverride, "no local runtime chart")
}

func resolvePublishedDevopsDeploySpecWithReason(ctx Context, target OpenResult, versionOverride, reason string) (DeploySpec, error) {
	registry := publishedDevopsChartRegistry(target)
	version := strings.TrimSpace(versionOverride)
	if version == "" {
		version = strings.TrimSpace(target.EnvConfig.RuntimeVersion)
	}
	if version == "" {
		// Dry-run contract: the decision that stops the plan must surface as a
		// trace line before the error return.
		ctx.Trace("deploy: " + reason + " and no runtime version resolved; cannot deploy the published " + DevopsComponentName + " chart")
		return DeploySpec{}, fmt.Errorf("runtime version is required to deploy the published %s chart: pass --version or persist runtimeversion in the env config", DevopsComponentName)
	}

	chartReference := publishedDevopsChartReference(registry)
	ctx.Trace("deploy: " + reason + "; using published chart " + chartReference + " version " + version)

	deployContext := KubernetesDeployContext{
		ComponentName: DevopsComponentName,
		ChartPath:     chartReference,
	}
	valuesFilePath := publishedDevopsValuesOverlayPath(ctx, target)
	deployInput, err := newHelmDeploySpecWithValues(target, deployContext, version, valuesFilePath)
	if err != nil {
		return DeploySpec{}, err
	}
	deployInput.ReleaseName = RuntimeReleaseName(target.Tenant)
	deployInput.UseHostCredentials = target.EnvConfig.HasAWSCloudAlias()
	deployInput.ContainerRegistry = registry
	if image := resolveRuntimeImageOverride(registry, version, target.EnvConfig.RuntimeImage); image != "" {
		ctx.Trace("deploy: runtime image override " + image + " (imageOverrides." + DevopsComponentName + ")")
		deployInput.ImageOverrides = map[string]string{DevopsComponentName: image}
	}

	return DeploySpec{
		Target:        target,
		DeployContext: deployContext,
		Deploy:        deployInput,
	}, nil
}

// publishedDevopsValuesOverlayPath finds the env's operator values overlay.
// A published chart has no local chart directory to hold the usual
// values.<env>.yaml, so the overlay lives beside the env config instead; its
// absence just means chart defaults and erun's --set list fully describe the
// deploy.
func publishedDevopsValuesOverlayPath(ctx Context, target OpenResult) string {
	configDir, err := ERunConfigDir()
	if err != nil {
		return ""
	}
	overlayPath := filepath.Join(configDir, target.Tenant, target.Environment, "values.yaml")
	if _, err := os.Stat(overlayPath); err != nil {
		return ""
	}
	ctx.Trace("deploy: applying values overlay " + overlayPath)
	return overlayPath
}

// publishedDevopsChartRegistry prefers the env's recorded RuntimeRegistry so a
// reopen keeps addressing the registry the env was deployed from.
func publishedDevopsChartRegistry(target OpenResult) string {
	if registry := strings.TrimSpace(target.EnvConfig.RuntimeRegistry); registry != "" {
		return registry
	}
	if registry, ok := target.EnvConfig.ContainerRegistries.DeployRegistry(); ok {
		return registry
	}
	if registry := resolveProjectContainerRegistry(target.RepoPath, target.Environment); registry != "" {
		return registry
	}
	return DefaultContainerRegistry
}

// PublishedChartNotFoundError reports that the published runtime chart a remote
// deploy resolved to could not be pulled at the resolved version. The usual
// cause is a version whose runtime image was pushed to the registry — so the
// version picker offered it — but whose chart the release flow never published
// (snapshots and unreleased versions are not published as charts), or a chart
// tag that has since been pruned. It replaces helm's opaque chart-pull exit
// status with an actionable message naming the version and registry.
type PublishedChartNotFoundError struct {
	ChartReference string
	Version        string
	Registry       string
	HelmOutput     string
	Err            error
}

func (e *PublishedChartNotFoundError) Error() string {
	msg := "runtime chart " + strings.TrimSpace(e.ChartReference) + " version " + strings.TrimSpace(e.Version) + " could not be pulled"
	if registry := strings.TrimSpace(e.Registry); registry != "" {
		msg += " from " + registry
	}
	msg += ": that version has no published chart in the registry. " +
		"`erun push` publishes a version's image and chart together, so a version is deployable only after it is pushed — " +
		"run `erun push --version " + strings.TrimSpace(e.Version) + "` (or `erun release` for a release version), then deploy."
	if out := strings.TrimSpace(e.HelmOutput); out != "" {
		msg += "\n" + out
	}
	return msg
}

func (e *PublishedChartNotFoundError) Unwrap() error { return e.Err }

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
