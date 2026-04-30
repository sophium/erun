package eruncommon

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

func DockerImageBuilder(buildInput DockerBuildSpec, stdout, stderr io.Writer) error {
	if len(buildInput.Platforms) > 0 {
		if err := ensureDockerBuildxBuilder(buildInput.ContextDir, buildInput.Platforms, stdout, stderr); err != nil {
			return err
		}
	}
	cmd := exec.Command("docker", dockerBuildArgs(buildInput)...)
	cmd.Dir = buildInput.ContextDir
	output := new(bytes.Buffer)
	cmd.Stdout = dockerCommandOutputWriter(stdout, output)
	cmd.Stderr = dockerCommandOutputWriter(stderr, output)
	err := cmd.Run()
	if err == nil {
		return nil
	}

	message := output.String()
	if buildInput.Push && IsDockerPushAuthorizationError(message) {
		return DockerRegistryAuthError{
			Tag:      buildInput.Image.Tag,
			Registry: dockerRegistryFromImageTag(buildInput.Image.Tag),
			Message:  strings.TrimSpace(message),
			Err:      err,
		}
	}

	return err
}

func DockerImageExists(tag string) (bool, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return false, nil
	}
	cmd := exec.Command("docker", "image", "inspect", tag)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, nil
	}
	return false, err
}

func DockerManifestExists(tag string) (bool, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return false, nil
	}
	cmd := exec.Command("docker", "manifest", "inspect", tag)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, nil
	}
	return false, err
}

func dockerCommandOutputWriter(primary io.Writer, capture io.Writer) io.Writer {
	writers := make([]io.Writer, 0, 2)
	if primary != nil {
		writers = append(writers, primary)
	}
	if capture != nil {
		writers = append(writers, capture)
	}
	if len(writers) == 0 {
		return io.Discard
	}
	if len(writers) == 1 {
		return writers[0]
	}
	return io.MultiWriter(writers...)
}

func dockerBuildArgs(buildInput DockerBuildSpec) []string {
	tag := strings.TrimSpace(buildInput.Image.Tag)
	args := []string{"build"}
	if len(buildInput.Platforms) > 0 {
		args = []string{"buildx", "build", "--builder", multiPlatformBuildxBuilderName, "--platform", strings.Join(buildInput.Platforms, ",")}
	}
	args = append(args, "-t", tag)
	if version := dockerImageTagVersion(tag); version != "" {
		args = append(args, "--build-arg", "ERUN_VERSION="+version)
	}
	if buildInput.Push {
		args = append(args, "--push")
	}
	args = append(args, "-f", buildInput.DockerfilePath, ".")
	return args
}

func dockerBuildxSetupCommands(dir string) []commandSpec {
	return []commandSpec{
		{
			Dir:  dir,
			Name: "docker",
			Args: []string{"buildx", "inspect", multiPlatformBuildxBuilderName},
		},
		{
			Dir:  dir,
			Name: "docker",
			Args: []string{"buildx", "create", "--name", multiPlatformBuildxBuilderName, "--driver", "docker-container"},
		},
		{
			Dir:  dir,
			Name: "docker",
			Args: []string{"buildx", "inspect", "--builder", multiPlatformBuildxBuilderName, "--bootstrap"},
		},
	}
}

var buildxPlatformsPattern = regexp.MustCompile(`(?m)^\s*Platforms:\s*(.+)$`)

func ensureDockerBuildxBuilder(dir string, requiredPlatforms []string, stdout, stderr io.Writer) error {
	inspect := exec.Command("docker", "buildx", "inspect", multiPlatformBuildxBuilderName)
	inspect.Dir = dir
	inspect.Stdout = io.Discard
	inspect.Stderr = io.Discard
	if err := inspect.Run(); err != nil {
		create := exec.Command("docker", "buildx", "create", "--name", multiPlatformBuildxBuilderName, "--driver", "docker-container")
		create.Dir = dir
		create.Stdout = stdout
		create.Stderr = stderr
		if err := create.Run(); err != nil {
			return err
		}
	}

	bootstrap := exec.Command("docker", "buildx", "inspect", "--builder", multiPlatformBuildxBuilderName, "--bootstrap")
	bootstrap.Dir = dir
	output := new(bytes.Buffer)
	bootstrap.Stdout = io.MultiWriter(stdout, output)
	bootstrap.Stderr = io.MultiWriter(stderr, output)
	if err := bootstrap.Run(); err != nil {
		return err
	}
	if missingPlatforms := missingBuildxPlatforms(output.String(), requiredPlatforms); len(missingPlatforms) > 0 {
		availablePlatforms := buildxPlatforms(output.String())
		if len(availablePlatforms) == 0 {
			return fmt.Errorf("multi-platform release builder %q did not report supported platforms after bootstrap", multiPlatformBuildxBuilderName)
		}
		return fmt.Errorf("multi-platform release builder %q does not support required platforms: %s (available: %s)", multiPlatformBuildxBuilderName, strings.Join(missingPlatforms, ", "), strings.Join(availablePlatforms, ", "))
	}
	return nil
}

func buildxPlatforms(output string) []string {
	match := buildxPlatformsPattern.FindStringSubmatch(output)
	if len(match) < 2 {
		return nil
	}
	rawPlatforms := strings.Split(match[1], ",")
	platforms := make([]string, 0, len(rawPlatforms))
	for _, platform := range rawPlatforms {
		platform = strings.TrimSpace(platform)
		if platform == "" {
			continue
		}
		platforms = append(platforms, platform)
	}
	return platforms
}

func missingBuildxPlatforms(output string, requiredPlatforms []string) []string {
	if len(requiredPlatforms) == 0 {
		return nil
	}
	supported := make(map[string]struct{}, len(requiredPlatforms))
	for _, platform := range buildxPlatforms(output) {
		supported[platform] = struct{}{}
	}
	missing := make([]string, 0, len(requiredPlatforms))
	for _, platform := range requiredPlatforms {
		platform = strings.TrimSpace(platform)
		if platform == "" {
			continue
		}
		if _, ok := supported[platform]; ok {
			continue
		}
		missing = append(missing, platform)
	}
	return missing
}

func dockerImageTagVersion(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return ""
	}
	index := strings.LastIndex(tag, ":")
	if index < 0 || index == len(tag)-1 {
		return ""
	}
	return tag[index+1:]
}

func BuildScriptRunner(dir, scriptPath string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.Command(scriptPath)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func DockerImagePusher(tag string, stdout, stderr io.Writer) error {
	pushCmd := exec.Command("docker", "push", tag)
	output := new(bytes.Buffer)
	pushCmd.Stdout = dockerCommandOutputWriter(stdout, output)
	pushCmd.Stderr = dockerCommandOutputWriter(stderr, output)
	err := pushCmd.Run()
	if err == nil {
		return nil
	}

	message := output.String()
	if IsDockerPushAuthorizationError(message) {
		return DockerRegistryAuthError{
			Tag:      tag,
			Registry: dockerRegistryFromImageTag(tag),
			Message:  strings.TrimSpace(message),
			Err:      err,
		}
	}

	return err
}

func DockerRegistryLogin(registry string, stdin io.Reader, stdout, stderr io.Writer) error {
	args := []string{"login"}
	if registry != "" {
		args = append(args, registry)
	}

	loginCmd := exec.Command("docker", args...)
	loginCmd.Stdin = stdin
	loginCmd.Stdout = stdout
	loginCmd.Stderr = stderr
	return loginCmd.Run()
}

func runScriptSpec(ctx Context, script scriptSpec, run BuildScriptRunnerFunc) error {
	if run == nil {
		run = BuildScriptRunner
	}
	name, args := scriptTraceCommand(script)
	ctx.TraceCommand(script.Dir, name, args...)
	if ctx.DryRun {
		return nil
	}
	return run(script.Dir, script.Path, script.Env, ctx.Stdin, ctx.Stdout, ctx.Stderr)
}

func runScriptSpecs(ctx Context, scripts []scriptSpec, run BuildScriptRunnerFunc) error {
	for _, script := range scripts {
		if err := runScriptSpec(ctx, script, run); err != nil {
			return err
		}
	}
	return nil
}

func buildScriptEnv(version string) []string {
	version = strings.TrimSpace(version)
	if version == "" {
		return nil
	}
	return []string{"ERUN_BUILD_VERSION=" + version}
}

func scriptTraceCommand(script scriptSpec) (string, []string) {
	if len(script.Env) == 0 {
		return script.Path, nil
	}

	args := append([]string{}, script.Env...)
	args = append(args, script.Path)
	return args[0], args[1:]
}
