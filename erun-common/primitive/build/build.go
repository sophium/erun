package build

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerbuild"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
	"github.com/sophium/erun/erun-common/primitive/shared/projectseam"
)

func ResolveCurrentDockerBuildSpecs(ctx core.Context, store projectseam.DockerStore, findProjectRoot projectseam.ProjectFinderFunc, resolveBuildContext projectseam.BuildContextResolverFunc, now projectseam.NowFunc, target model.DockerCommandTarget) ([]dockerspec.DockerBuildSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = dockerbuild.NormalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	buildContexts, err := ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, target)
	if err != nil {
		return nil, err
	}

	builds := make([]dockerspec.DockerBuildSpec, 0, len(buildContexts))
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

func BuildEnvDisablesBuildScript(store projectseam.DockerStore, findProjectRoot projectseam.ProjectFinderFunc, target model.DockerCommandTarget) bool {
	env := ResolveDockerBuildEnvConfig(store, findProjectRoot, target)
	return env != nil && env.DisableBuildScript
}

// RecommendBuildEnvIfMissing suggests a <tenant>-devops build environment; detection failures never block.
func RecommendBuildEnvIfMissing(ctx core.Context, findProjectRoot projectseam.ProjectFinderFunc, target model.DockerCommandTarget) {
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
	_, paths, ok, err := config.ResolveProjectComponent(projectRoot, selectedComponent)
	if err == nil && ok {
		return strings.TrimSpace(paths.Docker) != ""
	}
	if ok {
		return false
	}
	paths, err = config.LoadProjectPaths(projectRoot)
	return err == nil && strings.TrimSpace(paths.Docker) != ""
}

// TraceConfiguredBuildPaths traces configured build path overrides as dry-run decisions,
// and warns when the project config is gitignored.
func TraceConfiguredBuildPaths(ctx core.Context, findProjectRoot projectseam.ProjectFinderFunc, target model.DockerCommandTarget) {
	projectRoot, err := dockerbuild.ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil || strings.TrimSpace(projectRoot) == "" {
		return
	}
	config.WarnIfProjectConfigGitIgnored(ctx, projectRoot)

	name, paths, ok, err := config.ResolveProjectComponent(projectRoot, target.Component)
	if err != nil {
		ctx.Trace("build: component selection failed: " + err.Error())
		return
	}
	keyPrefix := "paths"
	if ok {
		ctx.Trace("build: component " + name + " selected (.erun/config.yaml components)")
		keyPrefix = "components." + name
	} else {
		paths, err = config.LoadProjectPaths(projectRoot)
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

func BuildExecutionSpecFromDockerBuilds(builds []dockerspec.DockerBuildSpec) dockerspec.BuildExecutionSpec {
	return dockerspec.BuildExecutionSpec{DockerBuilds: builds}
}

func dockerBuildsByTag(builds []dockerspec.DockerBuildSpec) map[string]dockerspec.DockerBuildSpec {
	buildsByTag := make(map[string]dockerspec.DockerBuildSpec, len(builds))
	for _, build := range builds {
		buildsByTag[strings.TrimSpace(build.Image.Tag)] = build
	}
	return buildsByTag
}

func ResolveDockerBuildTarget(findProjectRoot projectseam.ProjectFinderFunc, target model.DockerCommandTarget) (model.DockerCommandTarget, *model.ReleaseSpec, error) {
	target.VersionOverride = strings.TrimSpace(target.VersionOverride)
	if !target.Release {
		return target, nil, nil
	}
	if target.VersionOverride != "" {
		return model.DockerCommandTarget{}, nil, fmt.Errorf("release build cannot be combined with explicit version override")
	}
	if len(target.Platforms) > 0 {
		return model.DockerCommandTarget{}, nil, fmt.Errorf("release build cannot be combined with an explicit --platform override: a release always publishes every platform erun supports")
	}

	// A zero-value Logger drops traces.
	releaseSpec, err := ResolveReleaseSpec(core.Context{}, findProjectRoot, ReleaseParams{ProjectRoot: target.ProjectRoot, Force: target.Force})
	if err != nil {
		return model.DockerCommandTarget{}, nil, err
	}

	target.Release = false
	target.VersionOverride = releaseSpec.Version
	// A release builds every platform, regardless of any docker.platforms pin.
	target.Platforms = slices.Clone(dockerspec.MultiPlatformDockerBuilds)
	return target, &releaseSpec, nil
}
