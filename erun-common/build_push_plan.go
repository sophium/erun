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
		// push builds each image from its source context and pushes the
		// multi-platform manifest (per-arch tags + assembled manifest list),
		// the same path release uses. A multi-arch image only exists under
		// per-arch tags, so the bare version tag has no arch-less image to
		// docker push; RunDockerPushExecution skips the pushes entry once the
		// build is marked pushed.
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

	// push builds the image from its source context and pushes the multi-arch
	// manifest (per-arch + manifest list), not the bare version tag — a
	// multi-arch image has no arch-less tag to `docker push`. RunDockerPushSpec
	// returns after the build once Push is set, skipping the single-tag push.
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
