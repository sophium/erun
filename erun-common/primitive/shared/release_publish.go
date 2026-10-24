package shared

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/helm"
)

// ReleasePublisher is the publish half of a release: it puts the release
// version's images and charts in the registry, then proves they resolve. A
// release runs it between its local stages and its outward-facing ones, so the
// tag that announces a version is only ever pushed for a version that is
// already deployable.
// releasePublishTag is one image the publish step will push, alongside
// whether its registry is plain HTTP — `docker manifest` needs that spelled
// out on every invocation, unlike the daemon.
type ReleasePublishTag struct {
	Tag      string
	Insecure bool
}

type ReleasePublisher struct {
	// Tags names the image references the publish step will push. It is
	// checked against the release's own resolved images before anything is
	// mutated: a release whose images nothing publishes must fail while it is
	// still private, not after it has announced them.
	Tags    []ReleasePublishTag
	Publish func(core.Context) error
	Verify  func(core.Context) error
}

// ensureReleasePublishesResolvedImages refuses a release that would stamp and
// tag a version whose images nothing publishes. This is the guard that turns a
// silently git-only release into a loud failure, and it runs before the first
// stage so the refusal costs nothing to recover from.
func ensureReleasePublishesResolvedImages(spec ReleaseSpec, publisher ReleasePublisher) error {
	if len(spec.DockerImages) == 0 {
		return nil
	}

	publishable := make(map[string]struct{}, len(publisher.Tags))
	if publisher.Publish != nil && publisher.Verify != nil {
		for _, t := range publisher.Tags {
			publishable[strings.TrimSpace(t.Tag)] = struct{}{}
		}
	}

	missing := make([]string, 0, len(spec.DockerImages))
	for _, image := range spec.DockerImages {
		tag := strings.TrimSpace(image.Tag)
		if _, ok := publishable[tag]; !ok {
			missing = append(missing, tag)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("release %s resolved images that nothing in this run publishes: %s\na build publishes before the release tags, so it will not announce a version the registry never receives; run `erun build --release` from the project root so every release image resolves to a build",
		spec.Version, strings.Join(missing, ", "))
}

func runReleasePublication(ctx core.Context, publisher ReleasePublisher) error {
	if publisher.Publish == nil {
		return nil
	}
	reportAlreadyPublishedReleaseArtifacts(ctx, publisher.Tags)
	ctx.Trace("stage: publish")
	publishCtx, finishPublish := ctx.StartTimingStep("publish")
	err := publisher.Publish(publishCtx)
	finishPublish(err)
	if err != nil {
		return err
	}
	if publisher.Verify == nil {
		return nil
	}
	ctx.Trace("stage: verify-publication")
	verifyCtx, finishVerify := ctx.StartTimingStep("verify-publication")
	err = publisher.Verify(verifyCtx)
	finishVerify(err)
	return err
}

// reportAlreadyPublishedReleaseArtifacts probes the registry for each image
// this release resolves, before Publish does anything, so a release re-run
// after an interruption (the pod holding the local build/fingerprint
// cache was replaced mid-release) tells the operator up front what already
// landed, rather than depending on that cache to have survived and leaving
// the operator to infer progress from how quickly the rebuild finishes.
//
// This is reporting only — a single, non-retried existence probe — and does
// not change what gets rebuilt: that stays the fingerprint-based
// promote-and-skip decision inside Publish, which compares content, not just
// tag existence. A probe here cannot tell "already published with exactly
// this content" from "a tag with this name exists for some other reason", so
// it is not trusted to skip work, only to report.
func reportAlreadyPublishedReleaseArtifacts(ctx core.Context, tags []ReleasePublishTag) {
	for _, t := range tags {
		tag := strings.TrimSpace(t.Tag)
		if tag == "" {
			continue
		}
		ctx.TraceCommand("", "docker", append(DockerManifestArgs("inspect", t.Insecure), tag)...)
		if ctx.DryRun {
			continue
		}
		if dockerImageManifestResolves(ctx, tag, t.Insecure) {
			ctx.Info("==> Already published: " + tag)
		}
	}
}

// dockerImageManifestResolves is a single, non-retried existence probe: unlike
// readBackPublishedArtifact (used to confirm a tag this run just pushed, where
// a 404 is a transient read-after-write race worth retrying), a tag probed
// before anything is published is expected to 404 on a first release, and
// retrying that would slow down every release for the common case.
func dockerImageManifestResolves(ctx core.Context, tag string, insecure bool) bool {
	spec := helm.CommandSpec{Name: "docker", Args: append(DockerManifestArgs("inspect", insecure), tag)}
	_, err := RunCommandCapturingOutput(ctx, spec)
	return err == nil
}
