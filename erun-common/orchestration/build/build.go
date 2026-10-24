package build

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/primitive/build/fingerprint"
	"github.com/sophium/erun/erun-common/primitive/build/linuxpkg"
	"github.com/sophium/erun/erun-common/primitive/build/linuxpkgcheck"
	"github.com/sophium/erun/erun-common/primitive/build/releasespec"
	scriptpkg "github.com/sophium/erun/erun-common/primitive/build/script"
	"github.com/sophium/erun/erun-common/primitive/build/spec"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerbuild"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

func ResolveBuildExecution(ctx core.Context, store seam.DockerStore, findProjectRoot seam.ProjectFinderFunc, resolveBuildContext seam.BuildContextResolverFunc, now seam.NowFunc, target model.DockerCommandTarget) (dockerspec.BuildExecutionSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = dockerbuild.NormalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	spec.RecommendBuildEnvIfMissing(ctx, findProjectRoot, target)
	spec.TraceConfiguredBuildPaths(ctx, findProjectRoot, target)

	if spec.BuildEnvDisablesBuildScript(store, findProjectRoot, target) {
		target.DisableBuildScriptDiscovery = true
	}

	target, releaseSpec, script, err := resolveBuildExecutionTargetAndScript(findProjectRoot, target)
	if err != nil {
		return dockerspec.BuildExecutionSpec{}, err
	}
	if script != nil {
		script.Env = scriptpkg.BuildScriptEnv(target.VersionOverride)
		return dockerspec.BuildExecutionSpec{Release: releaseSpec, Script: script}, nil
	}

	linuxPackages, err := resolveLinuxPackagesForExecution(findProjectRoot, resolveBuildContext, target, releaseSpec)
	if err != nil {
		return dockerspec.BuildExecutionSpec{}, err
	}

	builds, err := spec.ResolveCurrentDockerBuildSpecs(ctx, store, findProjectRoot, resolveBuildContext, now, target)
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
	execution, err := fingerprint.ApplyIncrementalToBuildExecution(ctx, execution, noIncremental)
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

func resolveBuildExecutionTargetAndScript(findProjectRoot seam.ProjectFinderFunc, target model.DockerCommandTarget) (model.DockerCommandTarget, *model.ReleaseSpec, *dockerspec.ScriptSpec, error) {
	target, releaseSpec, err := releasespec.ResolveDockerBuildTarget(findProjectRoot, target)
	if err != nil {
		return model.DockerCommandTarget{}, nil, nil, err
	}
	script, err := scriptpkg.ResolveProjectRootBuildScript(findProjectRoot, target)
	if err != nil {
		return model.DockerCommandTarget{}, nil, nil, err
	}
	return target, releaseSpec, script, nil
}

func buildExecutionHasNoBuilds(linuxPackages []model.LinuxPackageSpec, builds []seam.DockerBuildSpec, releaseSpec *model.ReleaseSpec) bool {
	return len(linuxPackages) == 0 && len(builds) == 0 && releaseSpec == nil
}

// resolveLinuxPackagesForExecution resolves a plain build's packages and which
// reuse a recorded pass; releases export theirs instead.
func resolveLinuxPackagesForExecution(findProjectRoot seam.ProjectFinderFunc, resolveBuildContext seam.BuildContextResolverFunc, target model.DockerCommandTarget, releaseSpec *model.ReleaseSpec) ([]model.LinuxPackageSpec, error) {
	if releaseSpec != nil {
		return nil, nil
	}
	contexts, err := linuxpkg.ResolveCurrentLinuxPackageContexts(findProjectRoot, resolveBuildContext, target)
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
	if err := linuxpkgcheck.CheckLinuxPackagesBuildable(packages); err != nil {
		return nil, err
	}
	if target.NoIncremental {
		return packages, nil
	}
	return linuxpkgcheck.ApplyLinuxPackageReuse(packages), nil
}

func resolveBuildExecutionWithoutBuilds(findProjectRoot seam.ProjectFinderFunc, target model.DockerCommandTarget) (dockerspec.BuildExecutionSpec, error) {
	script, err := scriptpkg.ResolveNestedProjectBuildScript(findProjectRoot, target)
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
		return dockerspec.BuildExecutionSpec{}, spec.NewEmptyBuildPlanError(fmt.Sprintf(
			"the project has a docker build module at %s, but this build resolved no images from it, and running %s instead would report success without building an image; re-run from the project root or select the component whose images you meant to build",
			dockerDir, filepath.Join(filepath.Clean(script.Dir), script.Path)))
	}
	script.Env = scriptpkg.BuildScriptEnv(target.VersionOverride)
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
func releaseDockerPushSpecs(builds []seam.DockerBuildSpec) []model.DockerPushSpec {
	if len(builds) == 0 {
		return nil
	}

	pushes := make([]model.DockerPushSpec, 0, len(builds))
	for _, build := range builds {
		pushes = append(pushes, spec.NewDockerPushSpec(build.ContextDir, build.Image))
	}
	return pushes
}
