package eruncommon

import (
	"fmt"
	"strings"
)

func DockerPushExecutionSpecFromSpecs(builds []DockerBuildSpec, pushes []DockerPushSpec) DockerPushExecutionSpec {
	return DockerPushExecutionSpec{builds: builds, pushes: pushes}
}

func ResolveDockerPushExecution(ctx Context, store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, target DockerCommandTarget) (DockerPushExecutionSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = normalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	buildContexts, err := ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, target)
	if err != nil {
		return DockerPushExecutionSpec{}, err
	}

	builds := make([]DockerBuildSpec, 0, len(buildContexts))
	pushes := make([]DockerPushSpec, 0, len(buildContexts))
	for _, buildContext := range buildContexts {
		// A multi-arch image has no arch-less version tag to `docker push`, so the
		// build itself publishes the per-arch manifest list; the separate push
		// entry is skipped once the build is marked pushed.
		build, err := resolveDockerBuildSpec(store, findProjectRoot, resolveBuildContext, now, buildContext, target)
		if err != nil {
			return DockerPushExecutionSpec{}, err
		}
		build.Push = true
		builds = append(builds, build)
		pushes = append(pushes, NewDockerPushSpec(buildContext.Dir, build.Image))
	}

	builds, err = ApplyIncrementalToDockerBuilds(ctx, builds, target.NoIncremental)
	if err != nil {
		return DockerPushExecutionSpec{}, err
	}

	return DockerPushExecutionSpec{builds: builds, pushes: pushes}, nil
}

func ResolveDockerPushSpec(ctx Context, store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, target DockerCommandTarget) (DockerPushSpec, *DockerBuildSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = normalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	buildContext, err := resolveBuildContext()
	if err != nil {
		return DockerPushSpec{}, nil, err
	}
	if strings.TrimSpace(buildContext.DockerfilePath) == "" {
		return DockerPushSpec{}, nil, fmt.Errorf("dockerfile not found in current directory")
	}

	// A multi-arch image has no arch-less version tag to `docker push`, so the
	// build itself publishes the manifest; the single-tag push is skipped once
	// Push is set.
	resolvedBuild, err := resolveDockerBuildSpec(store, findProjectRoot, resolveBuildContext, now, buildContext, target)
	if err != nil {
		return DockerPushSpec{}, nil, err
	}
	resolvedBuild.Push = true
	incremental, err := ApplyIncrementalToDockerBuilds(ctx, []DockerBuildSpec{resolvedBuild}, target.NoIncremental)
	if err != nil {
		return DockerPushSpec{}, nil, err
	}
	resolvedBuild = incremental[0]

	return NewDockerPushSpec(buildContext.Dir, resolvedBuild.Image), &resolvedBuild, nil
}
