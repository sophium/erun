package execute

import (
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/gitx"
	buildrun "github.com/sophium/erun/erun-common/orchestration/build"
	deployrun "github.com/sophium/erun/erun-common/orchestration/deploy"
	releaserun "github.com/sophium/erun/erun-common/orchestration/release"
	buildpkg "github.com/sophium/erun/erun-common/primitive/build"
	deploypkg "github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/release"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// RunBuildExecution runs a plain `erun build`. store/deps are the cloud
// alias reader and dependencies ReportBuildOutcome uses to self-report this
// run to the erun platform, best-effort, once it finishes (erun#1954) --
// pass a nil store from a caller that has none (build never fails or
// changes its own output because reporting is unavailable).
func RunBuildExecution(ctx core.Context, execution shared.BuildExecutionSpec, runScript buildpkg.BuildScriptRunnerFunc, build buildpkg.DockerImageBuilderFunc, push buildpkg.DockerPushFunc, store config.CloudReadStore, deps cloud.CloudDependencies) (err error) {
	defer func() { reportBuildExecutionOutcome(ctx, execution, store, deps, err) }()
	ctx, finish := buildpkg.TraceBuildUmbrella(ctx)
	defer finish(&err)
	return runBuildExecution(ctx, execution, nil, nil, runScript, build, push, nil)
}

// RunBuildExecutionAndDeploy is RunBuildExecution's `--deploy` counterpart;
// see its doc comment for store/deps.
func RunBuildExecutionAndDeploy(ctx core.Context, execution shared.BuildExecutionSpec, deploySpecs []deploypkg.DeploySpec, runScript buildpkg.BuildScriptRunnerFunc, build buildpkg.DockerImageBuilderFunc, push buildpkg.DockerPushFunc, deploy deploypkg.HelmChartDeployerFunc, store config.CloudReadStore, deps cloud.CloudDependencies) (err error) {
	defer func() { reportBuildExecutionOutcome(ctx, execution, store, deps, err) }()
	ctx, finish := buildpkg.TraceBuildUmbrella(ctx)
	defer finish(&err)
	return runBuildExecution(ctx, execution, deploySpecs, nil, runScript, build, push, deploy)
}

// reportBuildExecutionOutcome self-reports one `erun build` run's outcome to
// the erun platform, best-effort -- see ReportBuildOutcome. RunReleaseExecution
// (the standalone `erun release` command) does not call this: a release
// records itself against a review through the release queue instead (see
// erun-docs/docs/collaboration/builds.md's "Release queue" section), so an
// automatic unattached report here would duplicate that record for no
// benefit. `erun build --release` (a snapshot vs. stable version choice, not
// a different command) still goes through RunBuildExecution above and does
// get reported, same as any other build.
func reportBuildExecutionOutcome(ctx core.Context, execution shared.BuildExecutionSpec, store config.CloudReadStore, deps cloud.CloudDependencies, err error) {
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

// buildExecutionProjectRootAndEnvironment reads the project root and
// environment name build already resolved off the first image in the
// execution -- every image in one execution shares both, since they are
// resolved once for the whole build. Both are empty for a project build
// script execution, which builds no docker images at all.
func buildExecutionProjectRootAndEnvironment(execution shared.BuildExecutionSpec) (string, string) {
	for _, build := range execution.DockerBuilds {
		if build.Image.ProjectRoot != "" || build.Image.Environment != "" {
			return build.Image.ProjectRoot, build.Image.Environment
		}
	}
	return "", ""
}

// RunReleaseExecution is a standalone `erun release`: the same build → publish →
// tag orchestration `erun build --release` performs, under the `==> Releasing`
// umbrella the desktop activity queue expects for a release rather than the
// `==> Building` one. Both entrypoints share one execution so the flow cannot
// drift between them.
func RunReleaseExecution(ctx core.Context, execution shared.BuildExecutionSpec, runGit gitx.GitCommandRunnerFunc, runScript buildpkg.BuildScriptRunnerFunc, build buildpkg.DockerImageBuilderFunc, push buildpkg.DockerPushFunc) (err error) {
	ctx, finish := release.TraceReleaseUmbrella(ctx, releaseExecutionVersion(execution))
	defer finish(&err)
	return runBuildExecution(ctx, execution, nil, runGit, runScript, build, push, nil)
}

// runBuildExecution is the last point before this run's exit code is decided.
// A plan that does nothing -- no release, no script, no linux package, no image,
// no chart -- runs no work and tests nothing, so reporting success for it turns
// "not tested" into "tested and green" for every caller reading the exit code.
// That precondition is checked here, before the execution it guards.
func runBuildExecution(ctx core.Context, execution shared.BuildExecutionSpec, deploySpecs []deploypkg.DeploySpec, runGit gitx.GitCommandRunnerFunc, runScript buildpkg.BuildScriptRunnerFunc, build buildpkg.DockerImageBuilderFunc, push buildpkg.DockerPushFunc, deploy deploypkg.HelmChartDeployerFunc) error {
	if !buildrun.BuildExecutionPlansWork(execution) {
		return buildpkg.NewEmptyBuildPlanError("the resolved plan has nothing to build or test")
	}
	return runResolvedBuildExecution(ctx, execution, deploySpecs, runGit, runScript, build, push, deploy)
}

func runResolvedBuildExecution(ctx core.Context, execution shared.BuildExecutionSpec, deploySpecs []deploypkg.DeploySpec, runGit gitx.GitCommandRunnerFunc, runScript buildpkg.BuildScriptRunnerFunc, build buildpkg.DockerImageBuilderFunc, push buildpkg.DockerPushFunc, deploy deploypkg.HelmChartDeployerFunc) error {
	if execution.Release != nil {
		// The release owns the publish rather than following it: its stages run
		// around the build+push so the version's images and charts exist, and
		// resolve, before the tag and branch pushes make it public.
		publisher := releaserun.NewReleasePublisher(execution, deploySpecs, runScript, build, push)
		if err := shared.RunReleaseSpec(ctx, *execution.Release, runGit, nil, &publisher); err != nil {
			return err
		}
	} else {
		// A release runs this via ensureReleaseReadyToPublish; a plain build has
		// to run it here. Builds are what actually fill the node between
		// releases, since every one of them grows the BuildKit cache.
		if err := buildrun.EnsureBuildDiskHeadroom(ctx); err != nil {
			return err
		}
		if _, err := buildrun.RunBuildExecutionBuilds(ctx, execution, deploySpecs, runScript, build, push); err != nil {
			return err
		}
	}
	// build+push above already published the images and runtime chart, so
	// these pure deploy specs only run helm.
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

// BuildExecutionReleaseSpec exposes the release a build execution carries, for
// transports that report the resolved plan alongside the run.
func BuildExecutionReleaseSpec(execution shared.BuildExecutionSpec) (shared.ReleaseSpec, bool) {
	if execution.Release == nil {
		return shared.ReleaseSpec{}, false
	}
	return *execution.Release, true
}

func releaseExecutionVersion(execution shared.BuildExecutionSpec) string {
	if execution.Release == nil {
		return ""
	}
	return execution.Release.Version
}
