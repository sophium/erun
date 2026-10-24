package execute

import (
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/cloud"
	buildrun "github.com/sophium/erun/erun-common/orchestration/build"
	deployrun "github.com/sophium/erun/erun-common/orchestration/deploy"
	releaserun "github.com/sophium/erun/erun-common/orchestration/release"
	buildpkg "github.com/sophium/erun/erun-common/primitive/build"
	deploypkg "github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/release"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
	"github.com/sophium/erun/erun-common/primitive/shared/releaseflow"
)

// RunBuildExecution runs a plain `erun build`; outcome reporting is best-effort and store may be nil.
func RunBuildExecution(ctx core.Context, execution dockerspec.BuildExecutionSpec, runScript seam.BuildScriptRunnerFunc, build seam.DockerImageBuilderFunc, push seam.DockerPushFunc, store config.CloudReadStore, deps cloud.CloudDependencies) (err error) {
	defer func() { reportBuildExecutionOutcome(ctx, execution, store, deps, err) }()
	ctx, finish := buildpkg.TraceBuildUmbrella(ctx)
	defer finish(&err)
	return runBuildExecution(ctx, execution, nil, nil, runScript, build, push, nil)
}

// RunBuildExecutionAndDeploy is RunBuildExecution's `--deploy` counterpart.
func RunBuildExecutionAndDeploy(ctx core.Context, execution dockerspec.BuildExecutionSpec, deploySpecs []seam.DeploySpec, runScript seam.BuildScriptRunnerFunc, build seam.DockerImageBuilderFunc, push seam.DockerPushFunc, deploy seam.HelmChartDeployerFunc, store config.CloudReadStore, deps cloud.CloudDependencies) (err error) {
	defer func() { reportBuildExecutionOutcome(ctx, execution, store, deps, err) }()
	ctx, finish := buildpkg.TraceBuildUmbrella(ctx)
	defer finish(&err)
	return runBuildExecution(ctx, execution, deploySpecs, nil, runScript, build, push, deploy)
}

// reportBuildExecutionOutcome reports a build best-effort. Standalone `erun release`
// skips it: a release records itself through the release queue.
func reportBuildExecutionOutcome(ctx core.Context, execution dockerspec.BuildExecutionSpec, store config.CloudReadStore, deps cloud.CloudDependencies, err error) {
	if store == nil {
		return
	}
	projectRoot, environment := buildExecutionProjectRootAndEnvironment(execution)
	failureDetail := ""
	if err != nil {
		failureDetail = err.Error()
	}
	buildpkg.ReportBuildOutcome(ctx, store, deps, buildpkg.ReportBuildOutcomeParams{
		ProjectRoot:   projectRoot,
		Environment:   environment,
		Version:       buildrun.NewBuildResult(execution).Version,
		Successful:    err == nil,
		FailureDetail: failureDetail,
		Profile:       buildrun.BuildExecutionProfile(ctx),
	})
}

// buildExecutionProjectRootAndEnvironment reads both off the first image; empty for a script build.
func buildExecutionProjectRootAndEnvironment(execution dockerspec.BuildExecutionSpec) (string, string) {
	for _, build := range execution.DockerBuilds {
		if build.Image.ProjectRoot != "" || build.Image.Environment != "" {
			return build.Image.ProjectRoot, build.Image.Environment
		}
	}
	return "", ""
}

// RunReleaseExecution runs a standalone `erun release` under the `==> Releasing` umbrella.
func RunReleaseExecution(ctx core.Context, execution dockerspec.BuildExecutionSpec, runGit seam.GitCommandRunnerFunc, runScript seam.BuildScriptRunnerFunc, build seam.DockerImageBuilderFunc, push seam.DockerPushFunc) (err error) {
	ctx, finish := release.TraceReleaseUmbrella(ctx, releaseExecutionVersion(execution))
	defer finish(&err)
	return runBuildExecution(ctx, execution, nil, runGit, runScript, build, push, nil)
}

// runBuildExecution refuses a plan with no work: exiting zero would report untested as green.
func runBuildExecution(ctx core.Context, execution dockerspec.BuildExecutionSpec, deploySpecs []seam.DeploySpec, runGit seam.GitCommandRunnerFunc, runScript seam.BuildScriptRunnerFunc, build seam.DockerImageBuilderFunc, push seam.DockerPushFunc, deploy seam.HelmChartDeployerFunc) error {
	if !buildrun.BuildExecutionPlansWork(execution) {
		return buildpkg.NewEmptyBuildPlanError("the resolved plan has nothing to build or test")
	}
	return runResolvedBuildExecution(ctx, execution, deploySpecs, runGit, runScript, build, push, deploy)
}

func runResolvedBuildExecution(ctx core.Context, execution dockerspec.BuildExecutionSpec, deploySpecs []seam.DeploySpec, runGit seam.GitCommandRunnerFunc, runScript seam.BuildScriptRunnerFunc, build seam.DockerImageBuilderFunc, push seam.DockerPushFunc, deploy seam.HelmChartDeployerFunc) error {
	if execution.Release != nil {
		// The release wraps build+push so images and charts exist before the tag is pushed.
		publisher := releaserun.NewReleasePublisher(execution, deploySpecs, runScript, build, push)
		if err := releaseflow.RunReleaseSpec(ctx, *execution.Release, runGit, nil, &publisher); err != nil {
			return err
		}
	} else {
		// A release checks headroom itself; builds are what grow the BuildKit cache between releases.
		if err := buildrun.EnsureBuildDiskHeadroom(ctx); err != nil {
			return err
		}
		if _, err := buildrun.RunBuildExecutionBuilds(ctx, execution, deploySpecs, runScript, build, push); err != nil {
			return err
		}
	}
	for _, deploySpec := range deploySpecs {
		if err := deploypkg.RunDeploySpec(ctx, deploySpec, deploy); err != nil {
			return err
		}
	}
	if execution.Release != nil {
		ctx.Info("release version: " + execution.Release.Version)
	}
	if version := deployrun.DeployedVersionForSpecs(deploySpecs); version != "" {
		ctx.Info("deployed version: " + version)
	}
	return nil
}

// BuildExecutionReleaseSpec returns the release a build execution carries.
func BuildExecutionReleaseSpec(execution dockerspec.BuildExecutionSpec) (model.ReleaseSpec, bool) {
	if execution.Release == nil {
		return model.ReleaseSpec{}, false
	}
	return *execution.Release, true
}

func releaseExecutionVersion(execution dockerspec.BuildExecutionSpec) string {
	if execution.Release == nil {
		return ""
	}
	return execution.Release.Version
}
