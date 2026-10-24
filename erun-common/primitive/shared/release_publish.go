package shared

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/helm"
)

// ReleasePublishTag is one image to push; Insecure must be spelled out to `docker manifest`.
type ReleasePublishTag struct {
	Tag      string
	Insecure bool
}

// ReleasePublisher publishes the version's images and charts and proves they resolve; the tag
// is pushed only after it succeeds.
type ReleasePublisher struct {
	// Tags is checked against the resolved images before anything is mutated.
	Tags    []ReleasePublishTag
	Publish func(core.Context) error
	Verify  func(core.Context) error
}

// ensureReleasePublishesResolvedImages refuses a version whose images nothing publishes, before the first stage.
func ensureReleasePublishesResolvedImages(spec model.ReleaseSpec, publisher ReleasePublisher) error {
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

// reportAlreadyPublishedReleaseArtifacts reports what a re-run already landed. Report only:
// existence is not content, so Publish's fingerprint decides what to rebuild.
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

// dockerImageManifestResolves is not retried: a 404 is expected on a first release.
func dockerImageManifestResolves(ctx core.Context, tag string, insecure bool) bool {
	spec := helm.CommandSpec{Name: "docker", Args: append(DockerManifestArgs("inspect", insecure), tag)}
	_, err := RunCommandCapturingOutput(ctx, spec)
	return err == nil
}
