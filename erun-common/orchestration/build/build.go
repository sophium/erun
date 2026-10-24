package build

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	buildpkg "github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerbuild"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
	"github.com/sophium/erun/erun-common/primitive/shared/projectseam"
)

func ResolveBuildExecution(ctx core.Context, store projectseam.DockerStore, findProjectRoot projectseam.ProjectFinderFunc, resolveBuildContext projectseam.BuildContextResolverFunc, now projectseam.NowFunc, target model.DockerCommandTarget) (dockerspec.BuildExecutionSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = dockerbuild.NormalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	buildpkg.RecommendBuildEnvIfMissing(ctx, findProjectRoot, target)
	buildpkg.TraceConfiguredBuildPaths(ctx, findProjectRoot, target)

	if buildpkg.BuildEnvDisablesBuildScript(store, findProjectRoot, target) {
		target.DisableBuildScriptDiscovery = true
	}

	target, releaseSpec, script, err := resolveBuildExecutionTargetAndScript(findProjectRoot, target)
	if err != nil {
		return dockerspec.BuildExecutionSpec{}, err
	}
	if script != nil {
		script.Env = buildpkg.BuildScriptEnv(target.VersionOverride)
		return dockerspec.BuildExecutionSpec{Release: releaseSpec, Script: script}, nil
	}

	linuxPackages, err := resolveLinuxPackagesForExecution(findProjectRoot, resolveBuildContext, target, releaseSpec)
	if err != nil {
		return dockerspec.BuildExecutionSpec{}, err
	}

	builds, err := buildpkg.ResolveCurrentDockerBuildSpecs(ctx, store, findProjectRoot, resolveBuildContext, now, target)
	if err != nil && !errors.Is(err, dockerspec.ErrDockerBuildContextNotFound) {
		return dockerspec.BuildExecutionSpec{}, err
	}

	if buildExecutionHasNoBuilds(linuxPackages, builds, releaseSpec) {
		return resolveBuildExecutionWithoutBuilds(findProjectRoot, target)
	}

	execution := dockerspec.BuildExecutionSpec{LinuxPackages: linuxPackages, DockerBuilds: builds}
	if releaseSpec != nil {
		execution = BuildExecutionSpecWithRelease(execution, *releaseSpec)
	}
	return finalizeBuildExecution(ctx, execution, target.NoIncremental)
}

// BuildExecutionPlansWork reports whether an execution has anything to run, so
// an empty run is not mistaken for a pass.
func BuildExecutionPlansWork(execution dockerspec.BuildExecutionSpec) bool {
	return execution.Release != nil ||
		execution.Script != nil ||
		len(execution.LinuxPackages) > 0 ||
		len(execution.DockerBuilds) > 0 ||
		len(execution.DockerPushes) > 0 ||
		len(execution.ComponentCharts) > 0
}

// finalizeBuildExecution resolves the chart source after promotion so final versions drive it.
func finalizeBuildExecution(ctx core.Context, execution dockerspec.BuildExecutionSpec, noIncremental bool) (dockerspec.BuildExecutionSpec, error) {
	execution, err := buildpkg.ApplyIncrementalToBuildExecution(ctx, execution, noIncremental)
	if err != nil {
		return dockerspec.BuildExecutionSpec{}, err
	}
	charts, err := resolveComponentChartSpecs(execution.DockerBuilds)
	if err != nil {
		return dockerspec.BuildExecutionSpec{}, err
	}
	execution.ComponentCharts = charts
	return execution, nil
}

func resolveBuildExecutionTargetAndScript(findProjectRoot projectseam.ProjectFinderFunc, target model.DockerCommandTarget) (model.DockerCommandTarget, *model.ReleaseSpec, *dockerspec.ScriptSpec, error) {
	target, releaseSpec, err := buildpkg.ResolveDockerBuildTarget(findProjectRoot, target)
	if err != nil {
		return model.DockerCommandTarget{}, nil, nil, err
	}
	script, err := buildpkg.ResolveProjectRootBuildScript(findProjectRoot, target)
	if err != nil {
		return model.DockerCommandTarget{}, nil, nil, err
	}
	return target, releaseSpec, script, nil
}

func buildExecutionHasNoBuilds(linuxPackages []model.LinuxPackageSpec, builds []dockerspec.DockerBuildSpec, releaseSpec *model.ReleaseSpec) bool {
	return len(linuxPackages) == 0 && len(builds) == 0 && releaseSpec == nil
}

// resolveLinuxPackagesForExecution resolves a plain build's packages and which
// reuse a recorded pass; releases export theirs instead.
func resolveLinuxPackagesForExecution(findProjectRoot projectseam.ProjectFinderFunc, resolveBuildContext projectseam.BuildContextResolverFunc, target model.DockerCommandTarget, releaseSpec *model.ReleaseSpec) ([]model.LinuxPackageSpec, error) {
	if releaseSpec != nil {
		return nil, nil
	}
	contexts, err := buildpkg.ResolveCurrentLinuxPackageContexts(findProjectRoot, resolveBuildContext, target)
	if errors.Is(err, dockerspec.ErrLinuxPackageBuildNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	projectRoot, err := dockerbuild.ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return nil, err
	}
	packages := dockerspec.NewLinuxPackageSpecs(projectRoot, contexts)
	if err := buildpkg.CheckLinuxPackagesBuildable(packages); err != nil {
		return nil, err
	}
	if target.NoIncremental {
		return packages, nil
	}
	return buildpkg.ApplyLinuxPackageReuse(packages), nil
}

func resolveBuildExecutionWithoutBuilds(findProjectRoot projectseam.ProjectFinderFunc, target model.DockerCommandTarget) (dockerspec.BuildExecutionSpec, error) {
	script, err := buildpkg.ResolveNestedProjectBuildScript(findProjectRoot, target)
	if err != nil {
		return dockerspec.BuildExecutionSpec{}, err
	}
	if script == nil {
		return dockerspec.BuildExecutionSpec{}, dockerspec.ErrDockerBuildContextNotFound
	}
	// With a docker build module present, zero images is a failure; running the
	// script instead would pass having built nothing.
	if dockerDir, ok, err := resolveProjectDockerModuleDir(findProjectRoot, target); err != nil {
		return dockerspec.BuildExecutionSpec{}, err
	} else if ok {
		return dockerspec.BuildExecutionSpec{}, buildpkg.NewEmptyBuildPlanError(fmt.Sprintf(
			"the project has a docker build module at %s, but this build resolved no images from it, and running %s instead would report success without building an image; re-run from the project root or select the component whose images you meant to build",
			dockerDir, filepath.Join(filepath.Clean(script.Dir), script.Path)))
	}
	script.Env = buildpkg.BuildScriptEnv(target.VersionOverride)
	return dockerspec.BuildExecutionSpec{Script: script}, nil
}

func BuildExecutionSpecWithRelease(execution dockerspec.BuildExecutionSpec, release model.ReleaseSpec) dockerspec.BuildExecutionSpec {
	execution.Release = &release
	if len(execution.DockerBuilds) > 0 && len(execution.DockerPushes) == 0 {
		execution.DockerPushes = releaseDockerPushSpecs(execution.DockerBuilds)
	}
	if len(execution.DockerBuilds) > 0 && len(execution.DockerPushes) > 0 {
		for i := range execution.DockerBuilds {
			execution.DockerBuilds[i].Push = true
		}
	}
	return execution
}

func BuildExecutionUsesBuildScript(execution dockerspec.BuildExecutionSpec) bool {
	return execution.Script != nil
}

// releaseDockerPushSpecs pushes every image, including version-pinned wrappers
// missing from release.DockerImages whose charts still need them; unchanged
// images are a cheap no-op.
func releaseDockerPushSpecs(builds []dockerspec.DockerBuildSpec) []dockerspec.DockerPushSpec {
	if len(builds) == 0 {
		return nil
	}

	pushes := make([]dockerspec.DockerPushSpec, 0, len(builds))
	for _, build := range builds {
		pushes = append(pushes, buildpkg.NewDockerPushSpec(build.ContextDir, build.Image))
	}
	return pushes
}
