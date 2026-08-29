package eruncommon

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

func ResolveCurrentDockerBuildSpecs(ctx Context, store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, target DockerCommandTarget) ([]DockerBuildSpec, error) {
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

func ResolveBuildExecution(ctx Context, store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, target DockerCommandTarget) (BuildExecutionSpec, error) {
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

	linuxBuilds, hadLinuxBuilds, err := resolveLinuxBuildsForExecution(findProjectRoot, resolveBuildContext, target, releaseSpec)
	if err != nil {
		return BuildExecutionSpec{}, err
	}

	builds, err := ResolveCurrentDockerBuildSpecs(ctx, store, findProjectRoot, resolveBuildContext, now, target)
	if err != nil && !errors.Is(err, ErrDockerBuildContextNotFound) {
		return BuildExecutionSpec{}, err
	}

	if buildExecutionHasNoBuilds(linuxBuilds, builds, releaseSpec) {
		return resolveBuildExecutionWithoutBuilds(findProjectRoot, target, hadLinuxBuilds)
	}

	execution := BuildExecutionSpec{linuxBuilds: linuxBuilds, dockerBuilds: builds, skippedLinux: hadLinuxBuilds && len(linuxBuilds) == 0}
	if releaseSpec != nil {
		execution = BuildExecutionSpecWithRelease(execution, *releaseSpec)
	}
	return finalizeBuildExecution(ctx, execution, target.NoIncremental)
}

// finalizeBuildExecution applies incremental promotion, then resolves the
// component chart source off the final builds so their release/snapshot version
// and registry drive the chart specs.
func finalizeBuildExecution(ctx Context, execution BuildExecutionSpec, noIncremental bool) (BuildExecutionSpec, error) {
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
func recommendBuildEnvIfMissing(ctx Context, findProjectRoot ProjectFinderFunc, target DockerCommandTarget) {
	projectRoot, err := resolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil || strings.TrimSpace(projectRoot) == "" {
		return
	}
	// A configured paths.docker override means the project has a build
	// environment at a non-conventional location, so the advisory would be wrong.
	if paths, err := loadProjectPaths(projectRoot); err == nil && strings.TrimSpace(paths.Docker) != "" {
		return
	}
	hasDevops, err := projectHasDevopsFolder(projectRoot)
	if err != nil || hasDevops {
		return
	}
	ctx.Info(`build: this project has no <tenant>-devops build environment — ask Claude to "init erun build environment" to set one up with the erun-build-env skill`)
}

// traceConfiguredBuildPaths surfaces configured paths.docker/paths.dockercontext/
// paths.version overrides as dry-run decision lines so the build plan shows the
// docker build root, build context, and version file were resolved from config
// rather than convention.
func traceConfiguredBuildPaths(ctx Context, findProjectRoot ProjectFinderFunc, target DockerCommandTarget) {
	projectRoot, err := resolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil || strings.TrimSpace(projectRoot) == "" {
		return
	}
	paths, err := loadProjectPaths(projectRoot)
	if err != nil {
		return
	}
	if v := strings.TrimSpace(paths.Docker); v != "" {
		ctx.Trace("build: docker build root configured as " + v + " (.erun/config.yaml paths.docker)")
	}
	if v := strings.TrimSpace(paths.DockerContext); v != "" {
		ctx.Trace("build: docker build context configured as " + v + " (.erun/config.yaml paths.dockercontext)")
	}
	if v := strings.TrimSpace(paths.Version); v != "" {
		ctx.Trace("build: version file configured as " + v + " (.erun/config.yaml paths.version)")
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

func buildExecutionHasNoBuilds(linuxBuilds []scriptSpec, builds []DockerBuildSpec, releaseSpec *ReleaseSpec) bool {
	return len(linuxBuilds) == 0 && len(builds) == 0 && releaseSpec == nil
}

func resolveLinuxBuildsForExecution(findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, target DockerCommandTarget, releaseSpec *ReleaseSpec) ([]scriptSpec, bool, error) {
	if releaseSpec != nil {
		return nil, false, nil
	}
	linuxBuilds, err := ResolveCurrentLinuxBuildScripts(findProjectRoot, resolveBuildContext, target, target.VersionOverride)
	if err != nil && !errors.Is(err, ErrLinuxPackageBuildNotFound) {
		return nil, false, err
	}
	hadLinuxBuilds := len(linuxBuilds) > 0
	if hadLinuxBuilds && !LinuxPackageBuildsSupported() {
		return nil, true, nil
	}
	return linuxBuilds, hadLinuxBuilds, nil
}

func resolveBuildExecutionWithoutBuilds(findProjectRoot ProjectFinderFunc, target DockerCommandTarget, hadLinuxBuilds bool) (BuildExecutionSpec, error) {
	if hadLinuxBuilds {
		return BuildExecutionSpec{skippedLinux: true}, nil
	}
	script, err := resolveNestedProjectBuildScript(findProjectRoot, target)
	if err != nil {
		return BuildExecutionSpec{}, err
	}
	if script == nil {
		return BuildExecutionSpec{}, ErrDockerBuildContextNotFound
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
		execution.dockerPushes = releaseDockerPushSpecs(execution.dockerBuilds, release.DockerImages)
	}
	if len(execution.dockerBuilds) > 0 && len(execution.dockerPushes) > 0 {
		releaseTags := make(map[string]struct{}, len(execution.dockerPushes))
		for _, pushInput := range execution.dockerPushes {
			releaseTags[strings.TrimSpace(pushInput.Image.Tag)] = struct{}{}
		}
		for i := range execution.dockerBuilds {
			if _, ok := releaseTags[strings.TrimSpace(execution.dockerBuilds[i].Image.Tag)]; !ok {
				continue
			}
			execution.dockerBuilds[i].Push = true
		}
	}
	return execution
}

func BuildExecutionUsesBuildScript(execution BuildExecutionSpec) bool {
	return execution.script != nil
}

func releaseDockerPushSpecs(builds []DockerBuildSpec, images []ReleaseDockerImageSpec) []DockerPushSpec {
	if len(builds) == 0 {
		return nil
	}

	releaseTags := make(map[string]struct{}, len(images))
	for _, image := range images {
		releaseTags[strings.TrimSpace(image.Tag)] = struct{}{}
	}
	releaseTags = expandLocalReleaseImageDependencies(builds, releaseTags)

	pushes := make([]DockerPushSpec, 0, len(releaseTags))
	for _, build := range builds {
		if _, ok := releaseTags[strings.TrimSpace(build.Image.Tag)]; !ok {
			continue
		}
		pushes = append(pushes, NewDockerPushSpec(build.ContextDir, build.Image))
	}
	return pushes
}

func expandLocalReleaseImageDependencies(builds []DockerBuildSpec, releaseTags map[string]struct{}) map[string]struct{} {
	if len(builds) == 0 || len(releaseTags) == 0 {
		return releaseTags
	}

	buildsByTag := dockerBuildsByTag(builds)
	expanded, queue := queuedReleaseTags(releaseTags)

	for len(queue) > 0 {
		tag := queue[0]
		queue = queue[1:]

		build, ok := buildsByTag[tag]
		if !ok {
			continue
		}
		for _, dependencyTag := range dockerfileLocalBaseImageTags(build.DockerfilePath, buildsByTag) {
			if _, exists := expanded[dependencyTag]; exists {
				continue
			}
			expanded[dependencyTag] = struct{}{}
			queue = append(queue, dependencyTag)
		}
	}

	for _, build := range builds {
		if !strings.Contains(strings.TrimSpace(build.Image.ImageName), "dind") {
			continue
		}
		expanded[strings.TrimSpace(build.Image.Tag)] = struct{}{}
	}

	return expanded
}

func dockerBuildsByTag(builds []DockerBuildSpec) map[string]DockerBuildSpec {
	buildsByTag := make(map[string]DockerBuildSpec, len(builds))
	for _, build := range builds {
		buildsByTag[strings.TrimSpace(build.Image.Tag)] = build
	}
	return buildsByTag
}

func queuedReleaseTags(releaseTags map[string]struct{}) (map[string]struct{}, []string) {
	expanded := make(map[string]struct{}, len(releaseTags))
	queue := make([]string, 0, len(releaseTags))
	for tag := range releaseTags {
		expanded[tag] = struct{}{}
		queue = append(queue, tag)
	}
	return expanded, queue
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
	releaseSpec, err := ResolveReleaseSpec(Context{}, findProjectRoot, ReleaseParams{ProjectRoot: target.ProjectRoot, Force: target.Force})
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
