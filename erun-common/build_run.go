package eruncommon

import (
	"fmt"
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
	for _, command := range buildInput.traceCommands() {
		ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	}
	if ctx.DryRun {
		return nil
	}
	return build(buildInput, ctx.Stdout, ctx.Stderr)
}

func shouldSkipDockerBuild(ctx Context, buildInput DockerBuildSpec, inspect DockerImageInspectorFunc) (bool, error) {
	if !buildInput.SkipIfExists {
		return false, nil
	}
	tag := strings.TrimSpace(buildInput.Image.Tag)
	if tag == "" {
		return false, nil
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
