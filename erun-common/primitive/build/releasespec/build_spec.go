package releasespec

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerbuild"
)

func ResolveDockerImageReference(ctx core.Context, store seam.DockerStore, findProjectRoot seam.ProjectFinderFunc, resolveBuildContext seam.BuildContextResolverFunc, now seam.NowFunc, buildDir string, target model.DockerCommandTarget) (model.DockerImageReference, error) {
	store, findProjectRoot, _, now = dockerbuild.NormalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	target, _, err := ResolveDockerBuildTarget(findProjectRoot, target)
	if err != nil {
		return model.DockerImageReference{}, err
	}

	projectRoot, err := dockerbuild.ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return model.DockerImageReference{}, err
	}

	environment, err := dockerbuild.ResolveDockerBuildEnvironment(store, findProjectRoot, projectRoot, target.Environment)
	if err != nil {
		return model.DockerImageReference{}, err
	}

	return dockerbuild.ResolveDockerImageReferenceForProject(ctx, now, projectRoot, environment, buildDir, strings.TrimSpace(target.VersionOverride), target.Component)
}
