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
