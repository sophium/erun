package build

import (
	"fmt"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/helm"
	buildpkg "github.com/sophium/erun/erun-common/primitive/build"
	pushpkg "github.com/sophium/erun/erun-common/primitive/push"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

// BuildExecutionProfile summarizes this run's timing tree; nil when no timing root is active.
func BuildExecutionProfile(ctx core.Context) *model.BuildProfileSummary {
	if ctx.Timing == nil {
		return nil
	}
	summary := core.SummarizeTimingRecordForProfile(ctx.Timing.ToRecord("build"))
	return &summary
}

func RunBuildExecutionBuilds(ctx core.Context, execution dockerspec.BuildExecutionSpec, deploySpecs []seam.DeploySpec, runScript seam.BuildScriptRunnerFunc, build seam.DockerImageBuilderFunc, push seam.DockerPushFunc) (map[string]struct{}, error) {
	pushedTags := make(map[string]struct{}, len(execution.DockerBuilds)+len(execution.DockerPushes))
	if execution.Script != nil {
		if len(deploySpecs) > 0 {
			return nil, fmt.Errorf("build deploy is not supported for project build scripts")
		}
		return pushedTags, buildpkg.RunScriptSpec(ctx, *execution.Script, runScript)
	}
	if execution.Release != nil {
		if err := buildpkg.ExportReleaseLinuxPackages(ctx, *execution.Release); err != nil {
			return nil, err
		}
	} else if err := buildpkg.RunLinuxPackageChecks(ctx, execution.LinuxPackages); err != nil {
		return nil, err
	}
	return runDockerBuildExecutionPhase(ctx, execution, deploySpecs, build, push, pushedTags)
}

func runDockerBuildExecutionPhase(ctx core.Context, execution dockerspec.BuildExecutionSpec, deploySpecs []seam.DeploySpec, build seam.DockerImageBuilderFunc, push seam.DockerPushFunc, pushedTags map[string]struct{}) (map[string]struct{}, error) {
	if len(execution.DockerPushes) > 0 {
		err := RunDockerPushExecution(ctx, dockerspec.DockerPushExecutionSpec{Builds: execution.DockerBuilds, Pushes: execution.DockerPushes, ComponentCharts: execution.ComponentCharts}, build, push)
		if err != nil {
			return pushedTags, err
		}
		return recordDockerPushTags(pushedTags, execution.DockerPushes), nil
	}
	if len(deploySpecs) > 0 {
		if err := buildAndPushDeployDockerImages(ctx, execution.DockerBuilds, build, push, pushedTags); err != nil {
			return pushedTags, err
		}
		// A builds-here --deploy deploys charts from the working tree, so validate rather than publish.
		return pushedTags, pushpkg.PackageComponentCharts(ctx, execution.ComponentCharts)
	}
	if err := buildpkg.RunDockerBuilds(ctx, execution.DockerBuilds, build); err != nil {
		return pushedTags, err
	}
	return pushedTags, pushpkg.PackageComponentCharts(ctx, execution.ComponentCharts)
}

func recordDockerPushTags(tags map[string]struct{}, pushes []model.DockerPushSpec) map[string]struct{} {
	for _, pushInput := range pushes {
		tags[pushInput.Image.Tag] = struct{}{}
	}
	return tags
}

func buildAndPushDeployDockerImages(ctx core.Context, builds []seam.DockerBuildSpec, build seam.DockerImageBuilderFunc, push seam.DockerPushFunc, pushedTags map[string]struct{}) error {
	if err := buildpkg.RunDockerBuildsSequentially(ctx, builds, build); err != nil {
		return err
	}
	for _, buildInput := range builds {
		pushInput := buildpkg.NewDockerPushSpec(buildInput.ContextDir, buildInput.Image)
		if err := RunDockerPushSpec(ctx, pushInput, nil, build, push); err != nil {
			return err
		}
		pushedTags[pushInput.Image.Tag] = struct{}{}
	}
	return nil
}

func RunDockerPushSpec(ctx core.Context, pushInput model.DockerPushSpec, buildInput *seam.DockerBuildSpec, build seam.DockerImageBuilderFunc, push seam.DockerPushFunc) error {
	if buildInput != nil {
		if err := buildpkg.RunDockerBuild(ctx, *buildInput, build); err != nil {
			return err
		}
		if buildInput.Push {
			return helm.PublishComponentChart(ctx, buildInput.Image, buildInput.Image.Version)
		}
	}
	if push == nil {
		push = func(ctx core.Context, pushInput model.DockerPushSpec) error {
			return pushpkg.RunDockerPush(ctx, pushInput, nil)
		}
	}
	if err := push(ctx, pushInput); err != nil {
		return err
	}
	return helm.PublishComponentChart(ctx, pushInput.Image, pushInput.Image.Version)
}

func RunDockerPushExecution(ctx core.Context, execution dockerspec.DockerPushExecutionSpec, build seam.DockerImageBuilderFunc, push seam.DockerPushFunc) error {
	if err := pushpkg.PreflightRegistryPushAccess(ctx, execution); err != nil {
		return err
	}
	if err := buildpkg.RunDockerBuildsSequentially(ctx, execution.Builds, build); err != nil {
		return err
	}
	builtAndPushedTags := make(map[string]struct{}, len(execution.Builds))
	for _, buildInput := range execution.Builds {
		if buildInput.Push {
			builtAndPushedTags[buildInput.Image.Tag] = struct{}{}
		}
	}
	for _, pushInput := range execution.Pushes {
		if _, ok := builtAndPushedTags[pushInput.Image.Tag]; ok {
			continue
		}
		if err := RunDockerPushSpec(ctx, pushInput, nil, build, push); err != nil {
			return err
		}
	}
	return pushpkg.PublishComponentCharts(ctx, execution.ComponentCharts)
}
