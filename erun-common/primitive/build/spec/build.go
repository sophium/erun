package spec

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config/projectpaths"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/buildtarget"
	"github.com/sophium/erun/erun-common/base/seam/imagebuild"
	"github.com/sophium/erun/erun-common/base/seam/projectroot"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerbuild"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

func ResolveCurrentDockerBuildSpecs(ctx core.Context, store buildtarget.DockerStore, findProjectRoot projectroot.ProjectFinderFunc, resolveBuildContext buildtarget.BuildContextResolverFunc, now buildtarget.NowFunc, target model.DockerCommandTarget) ([]imagebuild.DockerBuildSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = dockerbuild.NormalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	buildContexts, err := ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, target)
	if err != nil {
		return nil, err
	}

	builds := make([]imagebuild.DockerBuildSpec, 0, len(buildContexts))
	for _, buildContext := range buildContexts {
		build, err := ResolveDockerBuildSpec(ctx, store, findProjectRoot, resolveBuildContext, now, buildContext, target)
		if err != nil {
			return nil, err
		}
		builds = append(builds, build)
	}

	return builds, nil
}

// ErrBuildPlanEmpty reports a build that resolved no work at all.
var ErrBuildPlanEmpty = errors.New("build resolved nothing to build")

func NewEmptyBuildPlanError(reason string) error {
	return fmt.Errorf("%w: %s", ErrBuildPlanEmpty, reason)
}

func BuildEnvDisablesBuildScript(store buildtarget.DockerStore, findProjectRoot projectroot.ProjectFinderFunc, target model.DockerCommandTarget) bool {
	env := ResolveDockerBuildEnvConfig(store, findProjectRoot, target)
	return env != nil && env.DisableBuildScript
}

// RecommendBuildEnvIfMissing suggests a <tenant>-devops build environment; detection failures never block.
func RecommendBuildEnvIfMissing(ctx core.Context, findProjectRoot projectroot.ProjectFinderFunc, target model.DockerCommandTarget) {
	projectRoot, err := dockerbuild.ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil || strings.TrimSpace(projectRoot) == "" {
		return
	}
	if projectDeclaresDockerRoot(projectRoot, target.Component) {
		return
	}
	hasDevops, err := projectHasDevopsFolder(projectRoot)
	if err != nil || hasDevops {
		return
	}
	ctx.Info(`build: this project has no <tenant>-devops build environment — ask Claude to "init erun build environment" to set one up with the erun-build-env skill`)
}

// projectDeclaresDockerRoot reports whether config declares a non-conventional docker root.
func projectDeclaresDockerRoot(projectRoot, selectedComponent string) bool {
	_, paths, ok, err := projectpaths.ResolveProjectComponent(projectRoot, selectedComponent)
	if err == nil && ok {
		return strings.TrimSpace(paths.Docker) != ""
	}
	if ok {
		return false
	}
	paths, err = projectpaths.LoadProjectPaths(projectRoot)
	return err == nil && strings.TrimSpace(paths.Docker) != ""
}

// TraceConfiguredBuildPaths traces configured build path overrides as dry-run decisions,
// and warns when the project config is gitignored.
func TraceConfiguredBuildPaths(ctx core.Context, findProjectRoot projectroot.ProjectFinderFunc, target model.DockerCommandTarget) {
	projectRoot, err := dockerbuild.ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil || strings.TrimSpace(projectRoot) == "" {
		return
	}
	projectpaths.WarnIfProjectConfigGitIgnored(ctx, projectRoot)

	name, paths, ok, err := projectpaths.ResolveProjectComponent(projectRoot, target.Component)
	if err != nil {
		ctx.Trace("build: component selection failed: " + err.Error())
		return
	}
	keyPrefix := "paths"
	if ok {
		ctx.Trace("build: component " + name + " selected (.erun/config.yaml components)")
		keyPrefix = "components." + name
	} else {
		paths, err = projectpaths.LoadProjectPaths(projectRoot)
		if err != nil {
			return
		}
	}
	if v := strings.TrimSpace(paths.Docker); v != "" {
		ctx.Trace("build: docker build root configured as " + v + " (.erun/config.yaml " + keyPrefix + ".docker)")
	}
	if v := strings.TrimSpace(paths.DockerContext); v != "" {
		ctx.Trace("build: docker build context configured as " + v + " (.erun/config.yaml " + keyPrefix + ".dockercontext)")
	}
	if v := strings.TrimSpace(paths.Version); v != "" {
		ctx.Trace("build: version file configured as " + v + " (.erun/config.yaml " + keyPrefix + ".version)")
	}
}

func BuildExecutionSpecFromDockerBuilds(builds []imagebuild.DockerBuildSpec) dockerspec.BuildExecutionSpec {
	return dockerspec.BuildExecutionSpec{DockerBuilds: builds}
}
