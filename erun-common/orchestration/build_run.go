package orchestration

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/infra/helm"
	buildpkg "github.com/sophium/erun/erun-common/primitive/build"
	deploypkg "github.com/sophium/erun/erun-common/primitive/deploy"
	pushpkg "github.com/sophium/erun/erun-common/primitive/push"
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
		Version:       NewBuildResult(execution).Version,
		Successful:    err == nil,
		FailureDetail: failureDetail,
		Profile:       buildExecutionProfile(ctx),
	})
}

// buildExecutionProfile summarizes this run's own step-timing tree (see
// traceBuildUmbrella, which stashes the root on ctx.timing) into the bounded
// shape ReportBuildOutcome carries to the platform. nil when no timing root
// is active -- a dry run, or a build that hit an error before
// traceBuildUmbrella started one.
func buildExecutionProfile(ctx core.Context) *core.BuildProfileSummary {
	if ctx.Timing == nil {
		return nil
	}
	summary := core.SummarizeTimingRecordForProfile(ctx.Timing.ToRecord("build"))
	return &summary
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
	if !buildExecutionPlansWork(execution) {
		return buildpkg.NewEmptyBuildPlanError("the resolved plan has nothing to build or test")
	}
	return runResolvedBuildExecution(ctx, execution, deploySpecs, runGit, runScript, build, push, deploy)
}

func runResolvedBuildExecution(ctx core.Context, execution shared.BuildExecutionSpec, deploySpecs []deploypkg.DeploySpec, runGit gitx.GitCommandRunnerFunc, runScript buildpkg.BuildScriptRunnerFunc, build buildpkg.DockerImageBuilderFunc, push buildpkg.DockerPushFunc, deploy deploypkg.HelmChartDeployerFunc) error {
	if execution.Release != nil {
		// The release owns the publish rather than following it: its stages run
		// around the build+push so the version's images and charts exist, and
		// resolve, before the tag and branch pushes make it public.
		publisher := newReleasePublisher(execution, deploySpecs, runScript, build, push)
		if err := shared.RunReleaseSpec(ctx, *execution.Release, runGit, nil, &publisher); err != nil {
			return err
		}
	} else {
		// A release runs this via ensureReleaseReadyToPublish; a plain build has
		// to run it here. Builds are what actually fill the node between
		// releases, since every one of them grows the BuildKit cache.
		if err := ensureBuildDiskHeadroom(ctx); err != nil {
			return err
		}
		if _, err := runBuildExecutionBuilds(ctx, execution, deploySpecs, runScript, build, push); err != nil {
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
	if version := deployedVersionForSpecs(deploySpecs); version != "" {
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

func runBuildExecutionBuilds(ctx core.Context, execution shared.BuildExecutionSpec, deploySpecs []deploypkg.DeploySpec, runScript buildpkg.BuildScriptRunnerFunc, build buildpkg.DockerImageBuilderFunc, push buildpkg.DockerPushFunc) (map[string]struct{}, error) {
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

func runDockerBuildExecutionPhase(ctx core.Context, execution shared.BuildExecutionSpec, deploySpecs []deploypkg.DeploySpec, build buildpkg.DockerImageBuilderFunc, push buildpkg.DockerPushFunc, pushedTags map[string]struct{}) (map[string]struct{}, error) {
	if len(execution.DockerPushes) > 0 {
		err := RunDockerPushExecution(ctx, shared.DockerPushExecutionSpec{Builds: execution.DockerBuilds, Pushes: execution.DockerPushes, ComponentCharts: execution.ComponentCharts}, build, push)
		if err != nil {
			return pushedTags, err
		}
		return recordDockerPushTags(pushedTags, execution.DockerPushes), nil
	}
	if len(deploySpecs) > 0 {
		if err := buildAndPushDeployDockerImages(ctx, execution.DockerBuilds, build, push, pushedTags); err != nil {
			return pushedTags, err
		}
		// A builds-here --deploy pushes images but deploys charts from the working
		// tree, so package (validate) the charts rather than publish them.
		return pushedTags, pushpkg.PackageComponentCharts(ctx, execution.ComponentCharts)
	}
	if err := buildpkg.RunDockerBuilds(ctx, execution.DockerBuilds, build); err != nil {
		return pushedTags, err
	}
	return pushedTags, pushpkg.PackageComponentCharts(ctx, execution.ComponentCharts)
}

func recordDockerPushTags(tags map[string]struct{}, pushes []shared.DockerPushSpec) map[string]struct{} {
	for _, pushInput := range pushes {
		tags[pushInput.Image.Tag] = struct{}{}
	}
	return tags
}

func buildAndPushDeployDockerImages(ctx core.Context, builds []shared.DockerBuildSpec, build buildpkg.DockerImageBuilderFunc, push buildpkg.DockerPushFunc, pushedTags map[string]struct{}) error {
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

func deployedVersionForSpecs(specs []deploypkg.DeploySpec) string {
	version := ""
	for _, spec := range specs {
		current := strings.TrimSpace(spec.Deploy.Version)
		if current == "" {
			return ""
		}
		if version == "" {
			version = current
			continue
		}
		if current != version {
			return ""
		}
	}
	return version
}

func RunDockerPushSpec(ctx core.Context, pushInput shared.DockerPushSpec, buildInput *shared.DockerBuildSpec, build buildpkg.DockerImageBuilderFunc, push buildpkg.DockerPushFunc) error {
	if buildInput != nil {
		if err := buildpkg.RunDockerBuild(ctx, *buildInput, build); err != nil {
			return err
		}
		if buildInput.Push {
			// The multi-arch build already pushed the image, so only the chart
			// remains; a single-image push aligns chart and image versions.
			return helm.PublishComponentChart(ctx, buildInput.Image, buildInput.Image.Version)
		}
	}
	if push == nil {
		push = func(ctx core.Context, pushInput shared.DockerPushSpec) error {
			return pushpkg.RunDockerPush(ctx, pushInput, nil)
		}
	}
	if err := push(ctx, pushInput); err != nil {
		return err
	}
	return helm.PublishComponentChart(ctx, pushInput.Image, pushInput.Image.Version)
}

func RunDockerPushExecution(ctx core.Context, execution shared.DockerPushExecutionSpec, build buildpkg.DockerImageBuilderFunc, push buildpkg.DockerPushFunc) error {
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
	// Publish every chart under <tenant>-devops/k8s/* at the push/release version,
	// discovered by directory scan rather than keyed to same-named images, so
	// image-less component charts (a tenant's wrappers) publish too.
	return pushpkg.PublishComponentCharts(ctx, execution.ComponentCharts)
}
