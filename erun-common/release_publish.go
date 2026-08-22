package eruncommon

import (
	"fmt"
	"strings"
)

// ReleasePublisher is the publish half of a release: it puts the release
// version's images and charts in the registry, then proves they resolve. A
// release runs it between its local stages and its outward-facing ones, so the
// tag that announces a version is only ever pushed for a version that is
// already deployable.
type ReleasePublisher struct {
	// Tags names the image references the publish step will push. It is
	// checked against the release's own resolved images before anything is
	// mutated: a release whose images nothing publishes must fail while it is
	// still private, not after it has announced them.
	Tags    []string
	Publish func(Context) error
	Verify  func(Context) error
}

func newReleasePublisher(execution BuildExecutionSpec, deploySpecs []DeploySpec, runScript BuildScriptRunnerFunc, build DockerImageBuilderFunc, push DockerPushFunc) ReleasePublisher {
	tags := make([]string, 0, len(execution.dockerPushes))
	for _, pushInput := range execution.dockerPushes {
		tags = append(tags, strings.TrimSpace(pushInput.Image.Tag))
	}
	return ReleasePublisher{
		Tags: tags,
		Publish: func(publishCtx Context) error {
			_, err := runBuildExecutionBuilds(publishCtx, execution, deploySpecs, runScript, build, push)
			return err
		},
		Verify: func(verifyCtx Context) error {
			return verifyPublishedReleaseArtifacts(verifyCtx, execution)
		},
	}
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
		for _, tag := range publisher.Tags {
			publishable[strings.TrimSpace(tag)] = struct{}{}
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
	return fmt.Errorf("release %s resolved images that nothing in this run publishes: %s\na release publishes before it tags, so it will not announce a version the registry never receives; run `erun release` from the project root so every release image resolves to a build",
		spec.Version, strings.Join(missing, ", "))
}

func runReleasePublication(ctx Context, publisher ReleasePublisher) error {
	if publisher.Publish == nil {
		return nil
	}
	reportAlreadyPublishedReleaseArtifacts(ctx, publisher.Tags)
	ctx.Trace("stage: publish")
	if err := publisher.Publish(ctx); err != nil {
		return err
	}
	if publisher.Verify == nil {
		return nil
	}
	ctx.Trace("stage: verify-publication")
	return publisher.Verify(ctx)
}

// reportAlreadyPublishedReleaseArtifacts probes the registry for each image
// this release resolves, before Publish does anything, so a release re-run
// after an interruption (#1051: the pod holding the local build/fingerprint
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
func reportAlreadyPublishedReleaseArtifacts(ctx Context, tags []string) {
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		ctx.TraceCommand("", "docker", "manifest", "inspect", tag)
		if ctx.DryRun {
			continue
		}
		if dockerImageManifestResolves(ctx, tag) {
			ctx.Info("==> Already published: " + tag)
		}
	}
}

// dockerImageManifestResolves is a single, non-retried existence probe: unlike
// readBackPublishedArtifact (used to confirm a tag this run just pushed, where
// a 404 is a transient read-after-write race worth retrying), a tag probed
// before anything is published is expected to 404 on a first release, and
// retrying that would slow down every release for the common case.
func dockerImageManifestResolves(ctx Context, tag string) bool {
	spec := commandSpec{Name: "docker", Args: []string{"manifest", "inspect", tag}}
	_, err := runCommandCapturingOutput(ctx, spec)
	return err == nil
}

// verifyPublishedReleaseArtifacts closes the release's definition of done by
// resolving what it just pushed, so "the release completed" means "deploy
// --version <v> can pull this". Charts are already read back as they publish
// (publishComponentCharts), so only the image half needs a check here — nothing
// else proves a version's multi-arch manifest exists, and assuming it does is
// how a tag gets announced for an image no deploy can pull.
func verifyPublishedReleaseArtifacts(ctx Context, execution BuildExecutionSpec) error {
	for _, pushInput := range execution.dockerPushes {
		if err := VerifyPublishedDockerImage(ctx, pushInput.Image.Tag); err != nil {
			return err
		}
	}
	return nil
}
