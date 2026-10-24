package orchestration

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	buildpkg "github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	pushpkg "github.com/sophium/erun/erun-common/primitive/push"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func newReleasePublisher(execution shared.BuildExecutionSpec, deploySpecs []deploy.DeploySpec, runScript buildpkg.BuildScriptRunnerFunc, build buildpkg.DockerImageBuilderFunc, push buildpkg.DockerPushFunc) shared.ReleasePublisher {
	tags := make([]shared.ReleasePublishTag, 0, len(execution.DockerPushes))
	for _, pushInput := range execution.DockerPushes {
		tags = append(tags, shared.ReleasePublishTag{Tag: strings.TrimSpace(pushInput.Image.Tag), Insecure: pushInput.Image.Insecure})
	}
	return shared.ReleasePublisher{
		Tags: tags,
		Publish: func(publishCtx core.Context) error {
			_, err := runBuildExecutionBuilds(publishCtx, execution, deploySpecs, runScript, build, push)
			return err
		},
		Verify: func(verifyCtx core.Context) error {
			if err := verifyPublishedReleaseArtifacts(verifyCtx, execution); err != nil {
				return err
			}
			projectRoot := ""
			if execution.Release != nil {
				projectRoot = execution.Release.ProjectRoot
			}
			return verifyModuleReferencedImagesAnonymouslyPullable(verifyCtx, projectRoot, execution, shared.ProbeAnonymousManifestPull)
		},
	}
}

// verifyPublishedReleaseArtifacts closes the release's definition of done by
// resolving what it just pushed, so "the release completed" means "deploy
// --version <v> can pull this". Charts are already read back as they publish
// (publishComponentCharts), so only the image half needs a check here — nothing
// else proves a version's multi-arch manifest exists, and assuming it does is
// how a tag gets announced for an image no deploy can pull.
func verifyPublishedReleaseArtifacts(ctx core.Context, execution shared.BuildExecutionSpec) error {
	for _, pushInput := range execution.DockerPushes {
		if err := pushpkg.VerifyPublishedDockerImage(ctx, pushInput.Image.Tag, pushInput.Image.Insecure); err != nil {
			return err
		}
	}
	return nil
}
