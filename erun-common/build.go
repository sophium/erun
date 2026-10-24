package eruncommon

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

func ResolveCurrentDockerBuildSpecs(ctx core.Context, store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, target DockerCommandTarget) ([]DockerBuildSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = normalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	buildContexts, err := ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, target)
	if err != nil {
		return nil, err
	}

	builds := make([]DockerBuildSpec, 0, len(buildContexts))
	for _, buildContext := range buildContexts {
		build, err := resolveDockerBuildSpec(ctx, store, findProjectRoot, resolveBuildContext, now, buildContext, target)
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

func newEmptyBuildPlanError(reason string) error {
	return fmt.Errorf("%w: %s", ErrBuildPlanEmpty, reason)
}

func ResolveBuildExecution(ctx core.Context, store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, target DockerCommandTarget) (BuildExecutionSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = normalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	recommendBuildEnvIfMissing(ctx, findProjectRoot, target)
	traceConfiguredBuildPaths(ctx, findProjectRoot, target)

	if buildEnvDisablesBuildScript(store, findProjectRoot, target) {
		target.DisableBuildScriptDiscovery = true
	}

	target, releaseSpec, script, err := resolveBuildExecutionTargetAndScript(findProjectRoot, target)
	if err != nil {
		return BuildExecutionSpec{}, err
	}
	if script != nil {
		script.Env = buildScriptEnv(target.VersionOverride)
		return BuildExecutionSpec{release: releaseSpec, script: script}, nil
	}

	linuxPackages, err := resolveLinuxPackagesForExecution(findProjectRoot, resolveBuildContext, target, releaseSpec)
	if err != nil {
		return BuildExecutionSpec{}, err
	}

	builds, err := ResolveCurrentDockerBuildSpecs(ctx, store, findProjectRoot, resolveBuildContext, now, target)
	if err != nil && !errors.Is(err, ErrDockerBuildContextNotFound) {
		return BuildExecutionSpec{}, err
	}

	if buildExecutionHasNoBuilds(linuxPackages, builds, releaseSpec) {
		return resolveBuildExecutionWithoutBuilds(findProjectRoot, target)
	}

	execution := BuildExecutionSpec{linuxPackages: linuxPackages, dockerBuilds: builds}
	if releaseSpec != nil {
		execution = BuildExecutionSpecWithRelease(execution, *releaseSpec)
	}
	return finalizeBuildExecution(ctx, execution, target.NoIncremental)
}

// buildExecutionPlansWork reports whether an execution has anything to run. An
// execution that plans none of these does no work and tests nothing, so its
// success would be indistinguishable from a real pass.
func buildExecutionPlansWork(execution BuildExecutionSpec) bool {
	return execution.release != nil ||
		execution.script != nil ||
		len(execution.linuxPackages) > 0 ||
		len(execution.dockerBuilds) > 0 ||
		len(execution.dockerPushes) > 0 ||
		len(execution.componentCharts) > 0
}

// finalizeBuildExecution applies incremental promotion, then resolves the
// component chart source off the final builds so their release/snapshot version
// and registry drive the chart specs.
func finalizeBuildExecution(ctx core.Context, execution BuildExecutionSpec, noIncremental bool) (BuildExecutionSpec, error) {
	execution, err := ApplyIncrementalToBuildExecution(ctx, execution, noIncremental)
	if err != nil {
		return BuildExecutionSpec{}, err
	}
	charts, err := resolveComponentChartSpecs(execution.dockerBuilds)
	if err != nil {
		return BuildExecutionSpec{}, err
	}
	execution.componentCharts = charts
	return execution, nil
}

func buildEnvDisablesBuildScript(store DockerStore, findProjectRoot ProjectFinderFunc, target DockerCommandTarget) bool {
	env := ResolveDockerBuildEnvConfig(store, findProjectRoot, target)
	return env != nil && env.DisableBuildScript
}

// recommendBuildEnvIfMissing nudges a project that has no <tenant>-devops build
// environment toward creating one. Every build path funnels through
// ResolveBuildExecution, so the advisory fires even when a bare Dockerfile build
// would otherwise succeed. Best-effort: a detection failure never blocks the build.
func recommendBuildEnvIfMissing(ctx core.Context, findProjectRoot ProjectFinderFunc, target DockerCommandTarget) {
	projectRoot, err := ResolveDockerBuildProjectRoot(findProjectRoot, target)
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

// traceConfiguredBuildPaths surfaces the effective paths.docker/paths.dockercontext/
// paths.version overrides (or, when the project declares a components: map, the
// selected component and its docker/dockercontext/version) as dry-run decision
// lines so the build plan shows the docker build root, build context, and
// version file were resolved from config rather than convention. A bailout —
// an unknown or ambiguous --component selection — traces what was attempted
// before the resolution that follows fails loudly on it. It also warns when
// the project config itself is gitignored, since that silently voids the very
// overrides it just traced.
func traceConfiguredBuildPaths(ctx core.Context, findProjectRoot ProjectFinderFunc, target DockerCommandTarget) {
	projectRoot, err := ResolveDockerBuildProjectRoot(findProjectRoot, target)
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

func resolveBuildExecutionTargetAndScript(findProjectRoot ProjectFinderFunc, target DockerCommandTarget) (DockerCommandTarget, *ReleaseSpec, *scriptSpec, error) {
	target, releaseSpec, err := ResolveDockerBuildTarget(findProjectRoot, target)
	if err != nil {
		return DockerCommandTarget{}, nil, nil, err
	}
	script, err := resolveProjectRootBuildScript(findProjectRoot, target)
	if err != nil {
		return DockerCommandTarget{}, nil, nil, err
	}
	return target, releaseSpec, script, nil
}

func buildExecutionHasNoBuilds(linuxPackages []LinuxPackageSpec, builds []DockerBuildSpec, releaseSpec *ReleaseSpec) bool {
	return len(linuxPackages) == 0 && len(builds) == 0 && releaseSpec == nil
}

// resolveLinuxPackagesForExecution resolves the linux packages a plain build
// tests, and which of them reuse a recorded pass (none with --no-incremental,
// which also records none). A release resolves its own from the release root instead (see
// discoverReleaseLinuxPackages), because it exports them rather than testing
// them.
func resolveLinuxPackagesForExecution(findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, target DockerCommandTarget, releaseSpec *ReleaseSpec) ([]LinuxPackageSpec, error) {
	if releaseSpec != nil {
		return nil, nil
	}
	contexts, err := ResolveCurrentLinuxPackageContexts(findProjectRoot, resolveBuildContext, target)
	if errors.Is(err, ErrLinuxPackageBuildNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	projectRoot, err := ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return nil, err
	}
	packages := newLinuxPackageSpecs(projectRoot, contexts)
	if err := checkLinuxPackagesBuildable(packages); err != nil {
		return nil, err
	}
	if target.NoIncremental {
		return packages, nil
	}
	return applyLinuxPackageReuse(packages), nil
}

func resolveBuildExecutionWithoutBuilds(findProjectRoot ProjectFinderFunc, target DockerCommandTarget) (BuildExecutionSpec, error) {
	script, err := resolveNestedProjectBuildScript(findProjectRoot, target)
	if err != nil {
		return BuildExecutionSpec{}, err
	}
	if script == nil {
		return BuildExecutionSpec{}, ErrDockerBuildContextNotFound
	}
	// A nested build script only stands in for the image plan when the project
	// has no docker build module to resolve. When it does, resolving zero images
	// is a resolution failure, and running the script instead would exit zero
	// having built no image and run no gate -- a pass a caller cannot tell from
	// a real one.
	if dockerDir, ok, err := resolveProjectDockerModuleDir(findProjectRoot, target); err != nil {
		return BuildExecutionSpec{}, err
	} else if ok {
		return BuildExecutionSpec{}, newEmptyBuildPlanError(fmt.Sprintf(
			"the project has a docker build module at %s, but this build resolved no images from it, and running %s instead would report success without building an image; re-run from the project root or select the component whose images you meant to build",
			dockerDir, filepath.Join(filepath.Clean(script.Dir), script.Path)))
	}
	script.Env = buildScriptEnv(target.VersionOverride)
	return BuildExecutionSpec{script: script}, nil
}

func BuildExecutionSpecFromDockerBuilds(builds []DockerBuildSpec) BuildExecutionSpec {
	return BuildExecutionSpec{dockerBuilds: builds}
}

func BuildExecutionSpecWithRelease(execution BuildExecutionSpec, release ReleaseSpec) BuildExecutionSpec {
	execution.release = &release
	if len(execution.dockerBuilds) > 0 && len(execution.dockerPushes) == 0 {
		execution.dockerPushes = releaseDockerPushSpecs(execution.dockerBuilds)
	}
	if len(execution.dockerBuilds) > 0 && len(execution.dockerPushes) > 0 {
		for i := range execution.dockerBuilds {
			execution.dockerBuilds[i].Push = true
		}
	}
	return execution
}

func BuildExecutionUsesBuildScript(execution BuildExecutionSpec) bool {
	return execution.script != nil
}

// releaseDockerPushSpecs publishes every image a release builds, not only the
// images stamped with the release's own version. A version-pinned wrapper
// (erun-backend-postgres, erun-powerdns, erun-zitadel, erun-oci-registry — see
// erun-devops/AGENTS.md § "Wrapping And Pinning Third-Party Service Images")
// keeps its own VERSION file and is therefore never in release.DockerImages,
// but it is still a real release build whose chart is published and whose
// image must exist for that chart's deploy to work. Fingerprint promote-and-skip
// already makes a re-push of an unchanged pinned image a cheap no-op, so
// pushing everything costs nothing for the common case and closes the gap for
// the uncommon one: a wrapper published for the first time.
func releaseDockerPushSpecs(builds []DockerBuildSpec) []DockerPushSpec {
	if len(builds) == 0 {
		return nil
	}

	pushes := make([]DockerPushSpec, 0, len(builds))
	for _, build := range builds {
		pushes = append(pushes, NewDockerPushSpec(build.ContextDir, build.Image))
	}
	return pushes
}

func dockerBuildsByTag(builds []DockerBuildSpec) map[string]DockerBuildSpec {
	buildsByTag := make(map[string]DockerBuildSpec, len(builds))
	for _, build := range builds {
		buildsByTag[strings.TrimSpace(build.Image.Tag)] = build
	}
	return buildsByTag
}

func ResolveDockerBuildTarget(findProjectRoot ProjectFinderFunc, target DockerCommandTarget) (DockerCommandTarget, *ReleaseSpec, error) {
	target.VersionOverride = strings.TrimSpace(target.VersionOverride)
	if !target.Release {
		return target, nil, nil
	}
	if target.VersionOverride != "" {
		return DockerCommandTarget{}, nil, fmt.Errorf("release build cannot be combined with explicit version override")
	}
	if len(target.Platforms) > 0 {
		return DockerCommandTarget{}, nil, fmt.Errorf("release build cannot be combined with an explicit --platform override: a release always publishes every platform erun supports")
	}

	// Build callers don't surface a Context; the zero value is safe here because
	// a zero-value Logger silently drops traces.
	releaseSpec, err := ResolveReleaseSpec(core.Context{}, findProjectRoot, ReleaseParams{ProjectRoot: target.ProjectRoot, Force: target.Force})
	if err != nil {
		return DockerCommandTarget{}, nil, err
	}

	target.Release = false
	target.VersionOverride = releaseSpec.Version
	// A release publishes to any cluster, so it always builds every platform
	// erun supports, regardless of any per-environment docker.platforms pin.
	target.Platforms = slices.Clone(multiPlatformDockerBuilds)
	return target, &releaseSpec, nil
}
