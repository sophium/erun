package build

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func ResolveCurrentDockerBuildSpecs(ctx core.Context, store shared.DockerStore, findProjectRoot shared.ProjectFinderFunc, resolveBuildContext shared.BuildContextResolverFunc, now shared.NowFunc, target shared.DockerCommandTarget) ([]shared.DockerBuildSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = shared.NormalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	buildContexts, err := ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, target)
	if err != nil {
		return nil, err
	}

	builds := make([]shared.DockerBuildSpec, 0, len(buildContexts))
	for _, buildContext := range buildContexts {
		build, err := ResolveDockerBuildSpec(ctx, store, findProjectRoot, resolveBuildContext, now, buildContext, target)
		if err != nil {
			return nil, err
		}
		builds = append(builds, build)
	}

	return builds, nil
}

// ErrBuildPlanEmpty reports a build that resolved no work at all: no docker
// images, no linux packages, no project build script, no charts, no
// release. Nothing about such a plan is a result -- running it builds nothing
// and tests nothing -- so both the resolution and execution boundaries refuse it
// rather than letting the caller's exit code read as a successful build.
var ErrBuildPlanEmpty = errors.New("build resolved nothing to build")

func NewEmptyBuildPlanError(reason string) error {
	return fmt.Errorf("%w: %s", ErrBuildPlanEmpty, reason)
}

func BuildEnvDisablesBuildScript(store shared.DockerStore, findProjectRoot shared.ProjectFinderFunc, target shared.DockerCommandTarget) bool {
	env := ResolveDockerBuildEnvConfig(store, findProjectRoot, target)
	return env != nil && env.DisableBuildScript
}

// RecommendBuildEnvIfMissing nudges a project that has no <tenant>-devops build
// environment toward creating one. Every build path funnels through
// ResolveBuildExecution, so the advisory fires even when a bare Dockerfile build
// would otherwise succeed. Best-effort: a detection failure never blocks the build.
func RecommendBuildEnvIfMissing(ctx core.Context, findProjectRoot shared.ProjectFinderFunc, target shared.DockerCommandTarget) {
	projectRoot, err := shared.ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil || strings.TrimSpace(projectRoot) == "" {
		return
	}
	// A configured paths.docker override (or a selected components: entry) means
	// the project has a build environment at a non-conventional location, so the
	// advisory would be wrong.
	if projectDeclaresDockerRoot(projectRoot, target.Component) {
		return
	}
	hasDevops, err := projectHasDevopsFolder(projectRoot)
	if err != nil || hasDevops {
		return
	}
	ctx.Info(`build: this project has no <tenant>-devops build environment — ask Claude to "init erun build environment" to set one up with the erun-build-env skill`)
}

// projectDeclaresDockerRoot reports whether the project already declares a
// non-conventional docker build root — via the selected components: entry, or
// via the project-global paths.docker override when no component selection
// applies. Best-effort: an ambiguous/unknown component selection is not this
// advisory's concern — the real build resolution reports that loudly on its
// own, so this simply falls through to the unchanged paths.docker check.
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

// TraceConfiguredBuildPaths surfaces the effective paths.docker/paths.dockercontext/
// paths.version overrides (or, when the project declares a components: map, the
// selected component and its docker/dockercontext/version) as dry-run decision
// lines so the build plan shows the docker build root, build context, and
// version file were resolved from config rather than convention. A bailout —
// an unknown or ambiguous --component selection — traces what was attempted
// before the resolution that follows fails loudly on it. It also warns when
// the project config itself is gitignored, since that silently voids the very
// overrides it just traced.
func TraceConfiguredBuildPaths(ctx core.Context, findProjectRoot shared.ProjectFinderFunc, target shared.DockerCommandTarget) {
	projectRoot, err := shared.ResolveDockerBuildProjectRoot(findProjectRoot, target)
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

func BuildExecutionSpecFromDockerBuilds(builds []shared.DockerBuildSpec) shared.BuildExecutionSpec {
	return shared.BuildExecutionSpec{DockerBuilds: builds}
}

func dockerBuildsByTag(builds []shared.DockerBuildSpec) map[string]shared.DockerBuildSpec {
	buildsByTag := make(map[string]shared.DockerBuildSpec, len(builds))
	for _, build := range builds {
		buildsByTag[strings.TrimSpace(build.Image.Tag)] = build
	}
	return buildsByTag
}

func ResolveDockerBuildTarget(findProjectRoot shared.ProjectFinderFunc, target shared.DockerCommandTarget) (shared.DockerCommandTarget, *shared.ReleaseSpec, error) {
	target.VersionOverride = strings.TrimSpace(target.VersionOverride)
	if !target.Release {
		return target, nil, nil
	}
	if target.VersionOverride != "" {
		return shared.DockerCommandTarget{}, nil, fmt.Errorf("release build cannot be combined with explicit version override")
	}
	if len(target.Platforms) > 0 {
		return shared.DockerCommandTarget{}, nil, fmt.Errorf("release build cannot be combined with an explicit --platform override: a release always publishes every platform erun supports")
	}

	// Build callers don't surface a Context; the zero value is safe here because
	// a zero-value Logger silently drops traces.
	releaseSpec, err := shared.ResolveReleaseSpec(core.Context{}, findProjectRoot, shared.ReleaseParams{ProjectRoot: target.ProjectRoot, Force: target.Force})
	if err != nil {
		return shared.DockerCommandTarget{}, nil, err
	}

	target.Release = false
	target.VersionOverride = releaseSpec.Version
	// A release publishes to any cluster, so it always builds every platform
	// erun supports, regardless of any per-environment docker.platforms pin.
	target.Platforms = slices.Clone(shared.MultiPlatformDockerBuilds)
	return target, &releaseSpec, nil
}
