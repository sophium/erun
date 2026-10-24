package build

import (
	"path/filepath"
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

// ResolveDockerBuildForComponent resolves a single image by its build context directory name.
// It is not a components: selection, so version/context resolution stays project-global.
func ResolveDockerBuildForComponent(ctx core.Context, store seam.DockerStore, findProjectRoot seam.ProjectFinderFunc, resolveBuildContext seam.BuildContextResolverFunc, now seam.NowFunc, projectRoot, environment, componentName, versionOverride string, platformOverride []string) (*seam.DockerBuildSpec, error) {
	store, _, resolveBuildContext, now = dockerbuild.NormalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	if buildContext, ok := currentComponentDockerBuildContext(resolveBuildContext, componentName); ok {
		build, err := dockerbuild.NewDockerBuildSpec(ctx, store, now, projectRoot, environment, buildContext, versionOverride, platformOverride, "")
		if err != nil {
			return nil, err
		}
		return &build, nil
	}

	buildContext, ok, err := FindComponentDockerBuildContext(projectRoot, componentName)
	if err != nil || !ok {
		return nil, err
	}

	build, err := dockerbuild.NewDockerBuildSpec(ctx, store, now, projectRoot, environment, buildContext, versionOverride, platformOverride, "")
	if err != nil {
		return nil, err
	}
	return &build, nil
}

func currentComponentDockerBuildContext(resolveBuildContext seam.BuildContextResolverFunc, componentName string) (model.DockerBuildContext, bool) {
	if resolveBuildContext == nil {
		return model.DockerBuildContext{}, false
	}
	buildContext, err := resolveBuildContext()
	if err != nil {
		return model.DockerBuildContext{}, false
	}
	dir := filepath.Clean(strings.TrimSpace(buildContext.Dir))
	if filepath.Base(dir) != strings.TrimSpace(componentName) || filepath.Base(filepath.Dir(dir)) != "docker" {
		return model.DockerBuildContext{}, false
	}
	return buildContext, strings.TrimSpace(buildContext.DockerfilePath) != ""
}

func ResolveDockerBuildSpec(ctx core.Context, store seam.DockerStore, findProjectRoot seam.ProjectFinderFunc, resolveBuildContext seam.BuildContextResolverFunc, now seam.NowFunc, buildContext model.DockerBuildContext, target model.DockerCommandTarget) (seam.DockerBuildSpec, error) {
	projectRoot, err := dockerbuild.ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return seam.DockerBuildSpec{}, err
	}

	environment, err := dockerbuild.ResolveDockerBuildEnvironment(store, findProjectRoot, projectRoot, target.Environment)
	if err != nil {
		return seam.DockerBuildSpec{}, err
	}

	return dockerbuild.NewDockerBuildSpec(ctx, store, now, projectRoot, environment, buildContext, strings.TrimSpace(target.VersionOverride), target.Platforms, target.Component)
}

func NewDockerPushSpec(dir string, image model.DockerImageReference) model.DockerPushSpec {
	return model.DockerPushSpec{Dir: dir, Image: image}
}
