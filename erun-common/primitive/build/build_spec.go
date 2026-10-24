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

// ResolveDockerBuildForComponent resolves a single image by componentName —
// the name of the Docker build context directory anywhere in the project
// (MCP's existing single-image selector), not a components: map selection.
// It passes "" for newDockerBuildSpec's selectedComponent so version/context
// resolution stays on the project-global paths: block unless the project also
// declares a matching components: entry, which FindComponentDockerBuildContext
// (and currentComponentDockerBuildContext's own cwd match) already scope to.
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
