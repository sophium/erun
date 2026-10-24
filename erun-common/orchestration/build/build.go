package build

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/sophium/erun/erun-common/base/core"
	buildpkg "github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func ResolveBuildExecution(ctx core.Context, store shared.DockerStore, findProjectRoot shared.ProjectFinderFunc, resolveBuildContext shared.BuildContextResolverFunc, now shared.NowFunc, target shared.DockerCommandTarget) (shared.BuildExecutionSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = shared.NormalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	buildpkg.RecommendBuildEnvIfMissing(ctx, findProjectRoot, target)
	buildpkg.TraceConfiguredBuildPaths(ctx, findProjectRoot, target)

	if buildpkg.BuildEnvDisablesBuildScript(store, findProjectRoot, target) {
		target.DisableBuildScriptDiscovery = true
	}

	target, releaseSpec, script, err := resolveBuildExecutionTargetAndScript(findProjectRoot, target)
	if err != nil {
		return shared.BuildExecutionSpec{}, err
	}
	if script != nil {
		script.Env = buildpkg.BuildScriptEnv(target.VersionOverride)
		return shared.BuildExecutionSpec{Release: releaseSpec, Script: script}, nil
	}

	linuxPackages, err := resolveLinuxPackagesForExecution(findProjectRoot, resolveBuildContext, target, releaseSpec)
	if err != nil {
		return shared.BuildExecutionSpec{}, err
	}

	builds, err := buildpkg.ResolveCurrentDockerBuildSpecs(ctx, store, findProjectRoot, resolveBuildContext, now, target)
	if err != nil && !errors.Is(err, shared.ErrDockerBuildContextNotFound) {
		return shared.BuildExecutionSpec{}, err
	}

	if buildExecutionHasNoBuilds(linuxPackages, builds, releaseSpec) {
		return resolveBuildExecutionWithoutBuilds(findProjectRoot, target)
	}

	execution := shared.BuildExecutionSpec{LinuxPackages: linuxPackages, DockerBuilds: builds}
	if releaseSpec != nil {
		execution = BuildExecutionSpecWithRelease(execution, *releaseSpec)
	}
	return finalizeBuildExecution(ctx, execution, target.NoIncremental)
}

// BuildExecutionPlansWork reports whether an execution has anything to run. An
// execution that plans none of these does no work and tests nothing, so its
// success would be indistinguishable from a real pass.
func BuildExecutionPlansWork(execution shared.BuildExecutionSpec) bool {
	return execution.Release != nil ||
		execution.Script != nil ||
		len(execution.LinuxPackages) > 0 ||
		len(execution.DockerBuilds) > 0 ||
		len(execution.DockerPushes) > 0 ||
		len(execution.ComponentCharts) > 0
}

// finalizeBuildExecution applies incremental promotion, then resolves the
// component chart source off the final builds so their release/snapshot version
// and registry drive the chart specs.
func finalizeBuildExecution(ctx core.Context, execution shared.BuildExecutionSpec, noIncremental bool) (shared.BuildExecutionSpec, error) {
	execution, err := buildpkg.ApplyIncrementalToBuildExecution(ctx, execution, noIncremental)
	if err != nil {
		return shared.BuildExecutionSpec{}, err
	}
	charts, err := resolveComponentChartSpecs(execution.DockerBuilds)
	if err != nil {
		return shared.BuildExecutionSpec{}, err
	}
	execution.ComponentCharts = charts
	return execution, nil
}

func resolveBuildExecutionTargetAndScript(findProjectRoot shared.ProjectFinderFunc, target shared.DockerCommandTarget) (shared.DockerCommandTarget, *shared.ReleaseSpec, *shared.ScriptSpec, error) {
	target, releaseSpec, err := buildpkg.ResolveDockerBuildTarget(findProjectRoot, target)
	if err != nil {
		return shared.DockerCommandTarget{}, nil, nil, err
	}
	script, err := buildpkg.ResolveProjectRootBuildScript(findProjectRoot, target)
	if err != nil {
		return shared.DockerCommandTarget{}, nil, nil, err
	}
	return target, releaseSpec, script, nil
}

func buildExecutionHasNoBuilds(linuxPackages []shared.LinuxPackageSpec, builds []shared.DockerBuildSpec, releaseSpec *shared.ReleaseSpec) bool {
	return len(linuxPackages) == 0 && len(builds) == 0 && releaseSpec == nil
}

// resolveLinuxPackagesForExecution resolves the linux packages a plain build
// tests, and which of them reuse a recorded pass (none with --no-incremental,
// which also records none). A release resolves its own from the release root instead (see
// discoverReleaseLinuxPackages), because it exports them rather than testing
// them.
func resolveLinuxPackagesForExecution(findProjectRoot shared.ProjectFinderFunc, resolveBuildContext shared.BuildContextResolverFunc, target shared.DockerCommandTarget, releaseSpec *shared.ReleaseSpec) ([]shared.LinuxPackageSpec, error) {
	if releaseSpec != nil {
		return nil, nil
	}
	contexts, err := buildpkg.ResolveCurrentLinuxPackageContexts(findProjectRoot, resolveBuildContext, target)
	if errors.Is(err, shared.ErrLinuxPackageBuildNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	projectRoot, err := shared.ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return nil, err
	}
	packages := shared.NewLinuxPackageSpecs(projectRoot, contexts)
	if err := buildpkg.CheckLinuxPackagesBuildable(packages); err != nil {
		return nil, err
	}
	if target.NoIncremental {
		return packages, nil
	}
	return buildpkg.ApplyLinuxPackageReuse(packages), nil
}

func resolveBuildExecutionWithoutBuilds(findProjectRoot shared.ProjectFinderFunc, target shared.DockerCommandTarget) (shared.BuildExecutionSpec, error) {
	script, err := buildpkg.ResolveNestedProjectBuildScript(findProjectRoot, target)
	if err != nil {
		return shared.BuildExecutionSpec{}, err
	}
	if script == nil {
		return shared.BuildExecutionSpec{}, shared.ErrDockerBuildContextNotFound
	}
	// A nested build script only stands in for the image plan when the project
	// has no docker build module to resolve. When it does, resolving zero images
	// is a resolution failure, and running the script instead would exit zero
	// having built no image and run no gate -- a pass a caller cannot tell from
	// a real one.
	if dockerDir, ok, err := resolveProjectDockerModuleDir(findProjectRoot, target); err != nil {
		return shared.BuildExecutionSpec{}, err
	} else if ok {
		return shared.BuildExecutionSpec{}, buildpkg.NewEmptyBuildPlanError(fmt.Sprintf(
			"the project has a docker build module at %s, but this build resolved no images from it, and running %s instead would report success without building an image; re-run from the project root or select the component whose images you meant to build",
			dockerDir, filepath.Join(filepath.Clean(script.Dir), script.Path)))
	}
	script.Env = buildpkg.BuildScriptEnv(target.VersionOverride)
	return shared.BuildExecutionSpec{Script: script}, nil
}

func BuildExecutionSpecWithRelease(execution shared.BuildExecutionSpec, release shared.ReleaseSpec) shared.BuildExecutionSpec {
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

func BuildExecutionUsesBuildScript(execution shared.BuildExecutionSpec) bool {
	return execution.Script != nil
}

// releaseDockerPushSpecs publishes every image a release builds, not only the
// images stamped with the release's own version. A version-pinned wrapper
// (erun-backend-postgres, erun-powerdns, erun-zitadel, erun-oci-registry — see
// erun-devops/AGENTS.md § "Wrapping And Pinning Third-Party Service Images")
// keeps its own VERSION file and is therefore never in release.DockerImages,
// but it is still a real release build whose chart is published and whose
// image must exist for that chart's deploy to work. Fingerprint promote-and-skip
// already makes a re-push of an unchanged pinned image a cheap no-op, so
// pushing everything costs nothing for the common case and closes the gap for
// the uncommon one: a wrapper published for the first time.
func releaseDockerPushSpecs(builds []shared.DockerBuildSpec) []shared.DockerPushSpec {
	if len(builds) == 0 {
		return nil
	}

	pushes := make([]shared.DockerPushSpec, 0, len(builds))
	for _, build := range builds {
		pushes = append(pushes, buildpkg.NewDockerPushSpec(build.ContextDir, build.Image))
	}
	return pushes
}
