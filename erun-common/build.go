package eruncommon

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

func ResolveCurrentDockerBuildSpecs(store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, target DockerCommandTarget) ([]DockerBuildSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = normalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	buildContexts, err := ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, target)
	if err != nil {
		return nil, err
	}

	builds := make([]DockerBuildSpec, 0, len(buildContexts))
	for _, buildContext := range buildContexts {
		build, err := resolveDockerBuildSpec(store, findProjectRoot, resolveBuildContext, now, buildContext, target)
		if err != nil {
			return nil, err
		}
		builds = append(builds, build)
	}

	return builds, nil
}

func ResolveBuildExecution(store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, target DockerCommandTarget) (BuildExecutionSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = normalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

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

	builds, err := ResolveCurrentDockerBuildSpecs(store, findProjectRoot, resolveBuildContext, now, target)
	if err != nil && !errors.Is(err, ErrDockerBuildContextNotFound) {
		return BuildExecutionSpec{}, err
	}

	if buildExecutionHasNoBuilds(linuxBuilds, builds, releaseSpec) {
		return resolveBuildExecutionWithoutBuilds(findProjectRoot, target, hadLinuxBuilds)
	}

	execution := BuildExecutionSpec{linuxBuilds: linuxBuilds, dockerBuilds: builds, skippedLinux: hadLinuxBuilds && len(linuxBuilds) == 0}
	if releaseSpec != nil {
		return BuildExecutionSpecWithRelease(execution, *releaseSpec), nil
	}
	return execution, nil
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
			execution.dockerBuilds[i].Platforms = slices.Clone(multiPlatformDockerBuilds)
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

	releaseSpec, err := ResolveReleaseSpec(findProjectRoot, ReleaseParams{ProjectRoot: target.ProjectRoot, Force: target.Force})
	if err != nil {
		return DockerCommandTarget{}, nil, err
	}

	target.Release = false
	target.VersionOverride = releaseSpec.Version
	return target, &releaseSpec, nil
}
