package release

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	buildrun "github.com/sophium/erun/erun-common/orchestration/build"
	buildpkg "github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	pushpkg "github.com/sophium/erun/erun-common/primitive/push"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func NewReleasePublisher(execution shared.BuildExecutionSpec, deploySpecs []deploy.DeploySpec, runScript buildpkg.BuildScriptRunnerFunc, build buildpkg.DockerImageBuilderFunc, push buildpkg.DockerPushFunc) shared.ReleasePublisher {
	tags := make([]shared.ReleasePublishTag, 0, len(execution.DockerPushes))
	for _, pushInput := range execution.DockerPushes {
		tags = append(tags, shared.ReleasePublishTag{Tag: strings.TrimSpace(pushInput.Image.Tag), Insecure: pushInput.Image.Insecure})
	}
	return shared.ReleasePublisher{
		Tags: tags,
		Publish: func(publishCtx core.Context) error {
			_, err := buildrun.RunBuildExecutionBuilds(publishCtx, execution, deploySpecs, runScript, build, push)
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

// verifyPublishedReleaseArtifacts confirms each pushed image manifest resolves; charts
// are already read back as they publish.
func verifyPublishedReleaseArtifacts(ctx core.Context, execution shared.BuildExecutionSpec) error {
	for _, pushInput := range execution.DockerPushes {
		if err := pushpkg.VerifyPublishedDockerImage(ctx, pushInput.Image.Tag, pushInput.Image.Insecure); err != nil {
			return err
		}
	}
	return nil
}
