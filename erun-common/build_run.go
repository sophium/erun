package eruncommon

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func RunDockerBuild(ctx Context, buildInput DockerBuildSpec, build DockerImageBuilderFunc) error {
	return runDockerBuild(ctx, buildInput, build, nil)
}

func runDockerBuild(ctx Context, buildInput DockerBuildSpec, build DockerImageBuilderFunc, inspect DockerImageInspectorFunc) error {
	if build == nil {
		build = DockerImageBuilder
	}
	skip, err := shouldSkipDockerBuild(ctx, buildInput, inspect)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	traceIncrementalDecision(ctx, buildInput)
	for _, command := range buildInput.traceCommands() {
		ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	}
	if ctx.DryRun {
		return nil
	}
	return build(buildInput, ctx.Stdout, ctx.Stderr)
}

// traceIncrementalDecision mirrors shouldSkipDockerBuild's "<inspect> + <reason>"
// trace pattern for the fingerprint-based incremental path. The inspect was
// already executed during resolution (applyIncrementalPromotion), but emitting
// it here keeps dry-run output complete and parallels the SkipIfExists output
// style. Builds without a fingerprint (incremental disabled, or no fp tag was
// looked up) produce no extra trace.
func traceIncrementalDecision(ctx Context, buildInput DockerBuildSpec) {
	if buildInput.Fingerprint == "" {
		return
	}
	platforms := buildInput.Platforms
	if len(platforms) == 0 {
		platforms = []string{""}
	}
	for _, platform := range platforms {
		fpTag := fingerprintTag(buildInput.Image, buildInput.Fingerprint, platform)
		ctx.TraceCommand("", "docker", "image", "inspect", fpTag)
	}
	tag := strings.TrimSpace(buildInput.Image.Tag)
	if buildInput.Promote {
		ctx.Trace("promoting from cached fingerprint image: " + tag)
	} else {
		ctx.Trace("rebuilding because cached fingerprint image is missing or stale: " + tag)
	}
}

func shouldSkipDockerBuild(ctx Context, buildInput DockerBuildSpec, inspect DockerImageInspectorFunc) (bool, error) {
	if !buildInput.SkipIfExists {
		return false, nil
	}
	tag := strings.TrimSpace(buildInput.Image.Tag)
	if tag == "" {
		return false, nil
	}

	// For multi-platform builds verify the registry manifest actually covers every
	// required platform.  A single-arch manifest (or a manifest list that is missing
	// one of the platforms) must be rebuilt so downstream images can use it as a
	// multi-platform base image.
	if buildInput.Push && len(buildInput.Platforms) > 0 {
		ctx.TraceCommand("", "docker", "manifest", "inspect", tag)
		available, err := dockerManifestPlatforms(tag)
		if err != nil {
			return false, err
		}
		if !dockerPlatformsCovered(available, buildInput.Platforms) {
			return false, nil
		}
		// The registry copy is authoritative. If the local Docker store has a
		// single-arch copy of this tag, dependent builds resolving FROM <tag>
		// for another platform will fail with "no match for platform in
		// manifest" because the daemon prefers its local image and never falls
		// back to the registry. Drop the stale local copy so each per-platform
		// FROM resolution pulls fresh.
		if err := removeStaleLocalImageForPlatforms(ctx, tag, buildInput.Platforms); err != nil {
			return false, err
		}
		ctx.Trace("skipping docker build because configured multi-platform image exists: " + tag)
		return true, nil
	}

	inspectCommand := []string{"image", "inspect", tag}
	if inspect == nil {
		inspect = DockerImageExists
		if buildInput.Push {
			inspect = DockerManifestExists
			inspectCommand = []string{"manifest", "inspect", tag}
		}
	}

	ctx.TraceCommand("", "docker", inspectCommand...)
	exists, err := inspect(tag)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	ctx.Trace("skipping docker build because configured image exists: " + tag)
	return true, nil
}

func removeStaleLocalImageForPlatforms(ctx Context, tag string, required []string) error {
	localPlatforms, err := dockerLocalImagePlatforms(tag)
	if err != nil {
		return err
	}
	if len(localPlatforms) == 0 {
		return nil
	}
	if dockerPlatformsCovered(localPlatforms, required) {
		return nil
	}
	ctx.TraceCommand("", "docker", "image", "rm", tag)
	if ctx.DryRun {
		return nil
	}
	cmd := Command("docker", "image", "rm", tag)
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return err
	}
	return nil
}

// dockerPlatformsCovered reports whether every platform in required is present in available.
func dockerPlatformsCovered(available, required []string) bool {
	if len(available) == 0 {
		return false
	}
	supported := make(map[string]struct{}, len(available))
	for _, p := range available {
		supported[p] = struct{}{}
	}
	for _, p := range required {
		if _, ok := supported[strings.TrimSpace(p)]; !ok {
			return false
		}
	}
	return true
}

func RunDockerBuilds(ctx Context, builds []DockerBuildSpec, build DockerImageBuilderFunc) error {
	return runDockerBuilds(ctx, builds, build, nil)
}

func runDockerBuilds(ctx Context, builds []DockerBuildSpec, build DockerImageBuilderFunc, inspect DockerImageInspectorFunc) error {
	for _, buildInput := range orderedDockerBuildSpecs(builds) {
		if err := runDockerBuild(ctx, buildInput, build, inspect); err != nil {
			return err
		}
	}
	return nil
}

func RunBuildExecution(ctx Context, execution BuildExecutionSpec, runScript BuildScriptRunnerFunc, build DockerImageBuilderFunc, push DockerPushFunc) error {
	return runBuildExecution(ctx, execution, nil, runScript, build, push, nil)
}

func RunBuildExecutionAndDeploy(ctx Context, execution BuildExecutionSpec, deploySpecs []DeploySpec, runScript BuildScriptRunnerFunc, build DockerImageBuilderFunc, push DockerPushFunc, deploy HelmChartDeployerFunc) error {
	return runBuildExecution(ctx, execution, deploySpecs, runScript, build, push, deploy)
}

func runBuildExecution(ctx Context, execution BuildExecutionSpec, deploySpecs []DeploySpec, runScript BuildScriptRunnerFunc, build DockerImageBuilderFunc, push DockerPushFunc, deploy HelmChartDeployerFunc) error {
	if execution.release != nil {
		if err := RunReleaseSpec(ctx, *execution.release, nil, runScript); err != nil {
			return err
		}
	}
	if execution.skippedLinux {
		ctx.Trace("skipping linux package scripts: host is not Linux or dpkg-deb is unavailable")
	}

	pushedTags, err := runBuildExecutionBuilds(ctx, execution, deploySpecs, runScript, build, push)
	if err != nil {
		return err
	}
	for _, deploySpec := range filterDeploySpecsForPushedTags(deploySpecs, pushedTags) {
		if err := RunDeploySpec(ctx, deploySpec, build, push, deploy); err != nil {
			return err
		}
	}
	if execution.release != nil {
		ctx.Info("release version: " + execution.release.Version)
	}
	if version := deployedVersionForSpecs(deploySpecs); version != "" {
		ctx.Info("deployed version: " + version)
	}
	return nil
}

func runBuildExecutionBuilds(ctx Context, execution BuildExecutionSpec, deploySpecs []DeploySpec, runScript BuildScriptRunnerFunc, build DockerImageBuilderFunc, push DockerPushFunc) (map[string]struct{}, error) {
	pushedTags := make(map[string]struct{}, len(execution.dockerBuilds)+len(execution.dockerPushes))
	if execution.script != nil {
		if len(deploySpecs) > 0 {
			return nil, fmt.Errorf("build deploy is not supported for project build scripts")
		}
		return pushedTags, runScriptSpec(ctx, *execution.script, runScript)
	}
	if err := runScriptSpecs(ctx, execution.linuxBuilds, runScript); err != nil {
		return nil, err
	}
	return runDockerBuildExecutionPhase(ctx, execution, deploySpecs, build, push, pushedTags)
}

func runDockerBuildExecutionPhase(ctx Context, execution BuildExecutionSpec, deploySpecs []DeploySpec, build DockerImageBuilderFunc, push DockerPushFunc, pushedTags map[string]struct{}) (map[string]struct{}, error) {
	if len(execution.dockerPushes) > 0 {
		err := RunDockerPushExecution(ctx, DockerPushExecutionSpec{builds: execution.dockerBuilds, pushes: execution.dockerPushes}, build, push)
		if err != nil {
			return pushedTags, err
		}
		return recordDockerPushTags(pushedTags, execution.dockerPushes), nil
	}
	if len(deploySpecs) > 0 {
		return pushedTags, buildAndPushDeployDockerImages(ctx, execution.dockerBuilds, build, push, pushedTags)
	}
	return pushedTags, RunDockerBuilds(ctx, execution.dockerBuilds, build)
}

func recordDockerPushTags(tags map[string]struct{}, pushes []DockerPushSpec) map[string]struct{} {
	for _, pushInput := range pushes {
		tags[pushInput.Image.Tag] = struct{}{}
	}
	return tags
}

func buildAndPushDeployDockerImages(ctx Context, builds []DockerBuildSpec, build DockerImageBuilderFunc, push DockerPushFunc, pushedTags map[string]struct{}) error {
	if err := RunDockerBuilds(ctx, builds, build); err != nil {
		return err
	}
	for _, buildInput := range builds {
		pushInput := NewDockerPushSpec(buildInput.ContextDir, buildInput.Image)
		if err := RunDockerPushSpec(ctx, pushInput, nil, build, push); err != nil {
			return err
		}
		pushedTags[pushInput.Image.Tag] = struct{}{}
	}
	return nil
}

func filterDeploySpecsForPushedTags(specs []DeploySpec, pushedTags map[string]struct{}) []DeploySpec {
	if len(specs) == 0 || len(pushedTags) == 0 {
		return specs
	}

	filtered := make([]DeploySpec, 0, len(specs))
	for _, spec := range specs {
		copySpec := spec
		copySpec.Builds = filterDockerBuildsForPushedTags(spec.Builds, pushedTags)
		filtered = append(filtered, copySpec)
	}
	return filtered
}

func filterDockerBuildsForPushedTags(builds []DockerBuildSpec, pushedTags map[string]struct{}) []DockerBuildSpec {
	if len(builds) == 0 || len(pushedTags) == 0 {
		return builds
	}

	filtered := make([]DockerBuildSpec, 0, len(builds))
	for _, build := range builds {
		if _, ok := pushedTags[build.Image.Tag]; ok {
			continue
		}
		filtered = append(filtered, build)
	}
	return filtered
}

func deployedVersionForSpecs(specs []DeploySpec) string {
	version := ""
	for _, spec := range specs {
		current := strings.TrimSpace(spec.Deploy.Version)
		if current == "" {
			return ""
		}
		if version == "" {
			version = current
			continue
		}
		if current != version {
			return ""
		}
	}
	return version
}

func RunDockerPush(ctx Context, pushInput DockerPushSpec, push DockerImagePusherFunc) error {
	if push == nil {
		push = DockerImagePusher
	}
	command := pushInput.command()
	ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	if ctx.DryRun {
		return nil
	}
	return push(pushInput.Image.Tag, ctx.Stdout, ctx.Stderr)
}

func RunDockerPushSpec(ctx Context, pushInput DockerPushSpec, buildInput *DockerBuildSpec, build DockerImageBuilderFunc, push DockerPushFunc) error {
	if buildInput != nil {
		if err := runDockerBuild(ctx, *buildInput, build, nil); err != nil {
			return err
		}
		if buildInput.Push {
			return nil
		}
	}
	if push == nil {
		push = func(ctx Context, pushInput DockerPushSpec) error {
			return RunDockerPush(ctx, pushInput, nil)
		}
	}
	return push(ctx, pushInput)
}

func RunDockerPushExecution(ctx Context, execution DockerPushExecutionSpec, build DockerImageBuilderFunc, push DockerPushFunc) error {
	if err := RunDockerBuilds(ctx, execution.builds, build); err != nil {
		return err
	}
	builtAndPushedTags := make(map[string]struct{}, len(execution.builds))
	for _, buildInput := range execution.builds {
		if !buildInput.Push {
			continue
		}
		builtAndPushedTags[buildInput.Image.Tag] = struct{}{}
	}
	for _, pushInput := range execution.pushes {
		if _, ok := builtAndPushedTags[pushInput.Image.Tag]; ok {
			continue
		}
		if err := RunDockerPushSpec(ctx, pushInput, nil, build, push); err != nil {
			return err
		}
	}
	return nil
}
