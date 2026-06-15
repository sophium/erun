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
		imageRef, err := ResolveDockerImageReference(store, findProjectRoot, resolveBuildContext, now, buildContext.Dir, target)
		if err != nil {
			return DockerPushExecutionSpec{}, err
		}

		if imageRef.IsLocalBuild {
			build, err := resolveDockerBuildSpec(store, findProjectRoot, resolveBuildContext, now, buildContext, target)
			if err != nil {
				return DockerPushExecutionSpec{}, err
			}
			// Push through the build's own multi-platform push so the per-arch
			// images go up and a manifest list is assembled (pushMultiPlatformImage),
			// the same path release/deploy use. A locally-built multi-arch image
			// only exists under per-arch tags; pushing the bare version tag (the
			// pushes entry below) would fail "tag does not exist", so RunDockerPushExecution
			// skips it once the build is marked pushed.
			build.Push = true
			builds = append(builds, build)
			imageRef = build.Image
		}

		pushes = append(pushes, NewDockerPushSpec(buildContext.Dir, imageRef))
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

	imageRef, err := ResolveDockerImageReference(store, findProjectRoot, resolveBuildContext, now, buildContext.Dir, target)
	if err != nil {
		return DockerPushSpec{}, nil, err
	}

	var build *DockerBuildSpec
	if imageRef.IsLocalBuild {
		resolvedBuild, err := resolveDockerBuildSpec(store, findProjectRoot, resolveBuildContext, now, buildContext, target)
		if err != nil {
			return DockerPushSpec{}, nil, err
		}
		// Push via the build's multi-platform push (per-arch + manifest list),
		// not the bare version tag — a local multi-arch image has no arch-less
		// tag to `docker push`. RunDockerPushSpec returns after the build once
		// Push is set, skipping the redundant single-tag push.
		resolvedBuild.Push = true
		incremental, err := ApplyIncrementalToDockerBuilds(ctx, []DockerBuildSpec{resolvedBuild}, target.NoIncremental)
		if err != nil {
			return DockerPushSpec{}, nil, err
		}
		resolvedBuild = incremental[0]
		build = &resolvedBuild
		imageRef = resolvedBuild.Image
	}

	return NewDockerPushSpec(buildContext.Dir, imageRef), build, nil
}
