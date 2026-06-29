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

// resolvePublishedDevopsDeploySpec builds the runtime deploy spec for an
// environment with no local runtime chart: the published erun-devops chart,
// addressed by OCI reference and pinned to the env's runtime version (one
// version covers chart and image — they are published together at release).
// The env's RuntimeRegistry provenance wins over the published default so a
// reopen keeps addressing the registry the env was deployed from (#363); a
// custom EnvConfig.RuntimeImage rides in as imageOverrides.erun-devops.
//
// This replaces the embedded default-devops-chart copy that init used to
// scaffold per tenant — the copy had already drifted from the canonical
// chart (#510); the published chart is the single contract (#505).
func resolvePublishedDevopsDeploySpec(ctx Context, target OpenResult, versionOverride string) (DeploySpec, error) {
	// "no local runtime chart" is the reason the published chart is used on the
	// usual callers (remote envs, envs with no <tenant>-devops chart). The
	// runtime-image override caller passes its own reason, since the env there
	// does have a local chart it is deliberately bypassing (#697).
	return resolvePublishedDevopsDeploySpecWithReason(ctx, target, versionOverride, "no local runtime chart")
}

func resolvePublishedDevopsDeploySpecWithReason(ctx Context, target OpenResult, versionOverride, reason string) (DeploySpec, error) {
	registry := publishedDevopsChartRegistry(target)
	version := strings.TrimSpace(versionOverride)
	if version == "" {
		version = strings.TrimSpace(target.EnvConfig.RuntimeVersion)
	}
	if version == "" {
		// Bailout trace (dry-run contract): name the decision that stops the
		// plan before returning. This is the fresh-env path — no local chart
		// and no persisted/overridden runtime version — that a desktop create
		// must avoid by composing deploy at a built version (issue #644).
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

// publishedDevopsValuesOverlayPath looks for the env's operator values
// overlay next to its config (<ERunConfigDir>/<tenant>/<environment>/
// values.yaml). Repo-local charts carry their overlay as
// values.<env>.yaml in the chart directory; a published chart has no local
// chart directory, so the env config dir is the overlay's home. Absent file
// means no -f flag — the published chart's defaults plus erun's --set list
// fully describe the deploy.
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

// publishedDevopsChartRegistry picks the registry the published chart (and
// default image) is pulled from: the env's recorded RuntimeRegistry
// provenance, then the DEPLOY-marked registry of the project's registry list,
// then the public default.
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
// that already pins a tag or digest is complete and used verbatim. A reference
// without a tag is NOT complete — a registry path alone (e.g.
// ghcr.io/sophium/erun-devops) made the old code return it verbatim, so
// Kubernetes defaulted the pull to :latest, a tag the release flow never
// publishes (ImagePullBackOff). A tagless reference is therefore pinned to the
// env's runtime version, qualifying a bare image name (the historical
// `--runtime-image <name>` shape) against the env's registry first.
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

// imageReferenceHasTagOrDigest reports whether an image reference already pins
// a tag (a ":" in the final path segment) or a digest ("@sha256:..."). A ":"
// in the registry host (a port, e.g. localhost:5000/img) is not a tag, so the
// check looks only at the segment after the last "/".
func imageReferenceHasTagOrDigest(ref string) bool {
	if strings.Contains(ref, "@") {
		return true
	}
	lastSegment := ref[strings.LastIndex(ref, "/")+1:]
	return strings.Contains(lastSegment, ":")
}
