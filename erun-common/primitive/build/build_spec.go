package build

import (
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/helm"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func ResolveDockerImageReference(ctx core.Context, store shared.DockerStore, findProjectRoot shared.ProjectFinderFunc, resolveBuildContext shared.BuildContextResolverFunc, now shared.NowFunc, buildDir string, target shared.DockerCommandTarget) (helm.DockerImageReference, error) {
	store, findProjectRoot, _, now = shared.NormalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	target, _, err := ResolveDockerBuildTarget(findProjectRoot, target)
	if err != nil {
		return helm.DockerImageReference{}, err
	}

	projectRoot, err := shared.ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return helm.DockerImageReference{}, err
	}

	environment, err := shared.ResolveDockerBuildEnvironment(store, findProjectRoot, projectRoot, target.Environment)
	if err != nil {
		return helm.DockerImageReference{}, err
	}

	return shared.ResolveDockerImageReferenceForProject(ctx, now, projectRoot, environment, buildDir, strings.TrimSpace(target.VersionOverride), target.Component)
}

// ResolveDockerBuildForComponent resolves a single image by its build context directory name.
// It is not a components: selection, so version/context resolution stays project-global.
func ResolveDockerBuildForComponent(ctx core.Context, store shared.DockerStore, findProjectRoot shared.ProjectFinderFunc, resolveBuildContext shared.BuildContextResolverFunc, now shared.NowFunc, projectRoot, environment, componentName, versionOverride string, platformOverride []string) (*shared.DockerBuildSpec, error) {
	store, _, resolveBuildContext, now = shared.NormalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	if buildContext, ok := currentComponentDockerBuildContext(resolveBuildContext, componentName); ok {
		build, err := shared.NewDockerBuildSpec(ctx, store, now, projectRoot, environment, buildContext, versionOverride, platformOverride, "")
		if err != nil {
			return nil, err
		}
		return &build, nil
	}

	buildContext, ok, err := FindComponentDockerBuildContext(projectRoot, componentName)
	if err != nil || !ok {
		return nil, err
	}

	build, err := shared.NewDockerBuildSpec(ctx, store, now, projectRoot, environment, buildContext, versionOverride, platformOverride, "")
	if err != nil {
		return nil, err
	}
	return &build, nil
}

func currentComponentDockerBuildContext(resolveBuildContext shared.BuildContextResolverFunc, componentName string) (shared.DockerBuildContext, bool) {
	if resolveBuildContext == nil {
		return shared.DockerBuildContext{}, false
	}
	buildContext, err := resolveBuildContext()
	if err != nil {
		return shared.DockerBuildContext{}, false
	}
	dir := filepath.Clean(strings.TrimSpace(buildContext.Dir))
	if filepath.Base(dir) != strings.TrimSpace(componentName) || filepath.Base(filepath.Dir(dir)) != "docker" {
		return shared.DockerBuildContext{}, false
	}
	return buildContext, strings.TrimSpace(buildContext.DockerfilePath) != ""
}

func ResolveDockerBuildSpec(ctx core.Context, store shared.DockerStore, findProjectRoot shared.ProjectFinderFunc, resolveBuildContext shared.BuildContextResolverFunc, now shared.NowFunc, buildContext shared.DockerBuildContext, target shared.DockerCommandTarget) (shared.DockerBuildSpec, error) {
	projectRoot, err := shared.ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return shared.DockerBuildSpec{}, err
	}

	environment, err := shared.ResolveDockerBuildEnvironment(store, findProjectRoot, projectRoot, target.Environment)
	if err != nil {
		return shared.DockerBuildSpec{}, err
	}

	return shared.NewDockerBuildSpec(ctx, store, now, projectRoot, environment, buildContext, strings.TrimSpace(target.VersionOverride), target.Platforms, target.Component)
}

func NewDockerPushSpec(dir string, image helm.DockerImageReference) shared.DockerPushSpec {
	return shared.DockerPushSpec{Dir: dir, Image: image}
}
