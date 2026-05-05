package eruncommon

import (
	"bytes"
	"encoding/json"
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
	err := runDockerBuildOnce(buildInput, stdout, stderr)
	if err == nil {
		return nil
	}
	if buildInput.Push && shouldRetryAfterGHCRNamespaceLogin(err, buildInput.Image.Tag, stdout, stderr) {
		if retryErr := runDockerBuildOnce(buildInput, stdout, stderr); retryErr == nil {
			return nil
		} else {
			err = retryErr
		}
	}
	return err
}

func runDockerBuildOnce(buildInput DockerBuildSpec, stdout, stderr io.Writer) error {
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

func shouldRetryAfterGHCRNamespaceLogin(err error, tag string, stdout, stderr io.Writer) bool {
	var authErr DockerRegistryAuthError
	if !errors.As(err, &authErr) {
		return false
	}
	if !IsDockerCreatePackageDenied(authErr.Message) {
		return false
	}
	ok, loginErr := TryGHCRNamespaceLogin(tag, stdout, stderr)
	return loginErr == nil && ok
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

// dockerManifestPlatforms returns the platforms listed in a registry manifest for
// the given tag. Returns nil when the tag does not exist, is a single-arch image
// (no manifest list / OCI index), or when the manifest cannot be inspected.
func dockerManifestPlatforms(tag string) ([]string, error) {
	cmd := exec.Command("docker", "manifest", "inspect", tag)
	output, err := cmd.Output()
	if err != nil {
		return nil, nil // tag absent or inaccessible
	}
	var manifest struct {
		Manifests []struct {
			Platform struct {
				Architecture string `json:"architecture"`
				OS           string `json:"os"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal(output, &manifest); err != nil || len(manifest.Manifests) == 0 {
		return nil, nil // single-arch manifest or unrecognised format
	}
	platforms := make([]string, 0, len(manifest.Manifests))
	for _, m := range manifest.Manifests {
		if m.Platform.OS != "" && m.Platform.Architecture != "" {
			platforms = append(platforms, m.Platform.OS+"/"+m.Platform.Architecture)
		}
	}
	return platforms, nil
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
	// For local snapshot builds, also tag the image with the stable snapshot
	// identifier (e.g. "1.0.51-snapshot", no timestamp).  This lets wrapper
	// images (FROM …:${ERUN_VERSION}) always resolve the same local tag, keeping
	// the Docker build cache valid across pushes.  The stable tag is local-only
	// and is never included in the push step.
	if buildInput.Image.BaseVersion != "" {
		stableTag := fmt.Sprintf("%s/%s:%s",
			strings.TrimRight(buildInput.Image.Registry, "/"),
			buildInput.Image.ImageName,
			buildInput.Image.BaseVersion,
		)
		args = append(args, "-t", stableTag)
	}
	buildArgVersion := dockerImageTagVersion(tag)
	if buildInput.Image.BaseVersion != "" {
		buildArgVersion = buildInput.Image.BaseVersion
	}
	if buildArgVersion != "" {
		args = append(args, "--build-arg", "ERUN_VERSION="+buildArgVersion)
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
	err := runDockerPushOnce(tag, stdout, stderr)
	if err == nil {
		return nil
	}
	if shouldRetryAfterGHCRNamespaceLogin(err, tag, stdout, stderr) {
		if retryErr := runDockerPushOnce(tag, stdout, stderr); retryErr == nil {
			return nil
		} else {
			err = retryErr
		}
	}
	return err
}

func runDockerPushOnce(tag string, stdout, stderr io.Writer) error {
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
	if isGHCRRegistry(registry) {
		ok, err := tryGHCRLoginViaGH(registry, stdout, stderr)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}

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

func isGHCRRegistry(registry string) bool {
	registry = strings.ToLower(strings.TrimSpace(registry))
	return registry == "ghcr.io" || strings.HasPrefix(registry, "ghcr.io/")
}

func tryGHCRLoginViaGH(registry string, stdout, stderr io.Writer) (bool, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return false, nil
	}

	user, err := captureGHCommand("api", "user", "--jq", ".login")
	if err != nil {
		return false, nil
	}
	user = strings.TrimSpace(user)
	if user == "" {
		return false, nil
	}

	token, err := captureGHCommand("auth", "token")
	if err != nil {
		return false, nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false, nil
	}

	loginCmd := exec.Command("docker", "login", "ghcr.io", "-u", user, "--password-stdin")
	loginCmd.Stdin = strings.NewReader(token)
	loginCmd.Stdout = stdout
	loginCmd.Stderr = stderr
	if err := loginCmd.Run(); err != nil {
		return false, err
	}
	return true, nil
}

func captureGHCommand(args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// TryGHCRNamespaceLogin re-authenticates docker to ghcr.io as the GitHub user
// that owns the target namespace, when that user is also configured in the
// local gh CLI. Returns (true, nil) on a successful login, (false, nil) when
// the namespace cannot be impersonated (gh missing, account not logged in,
// non-ghcr.io tag), and (false, err) when the eventual docker login fails.
//
// Use this after a push failure with IsDockerCreatePackageDenied to attempt
// an automatic auth switch before falling back to an interactive prompt.
func TryGHCRNamespaceLogin(tag string, stdout, stderr io.Writer) (bool, error) {
	if !isGHCRRegistry(dockerRegistryFromImageTag(tag)) {
		return false, nil
	}
	namespace := DockerNamespaceFromTag(tag)
	if namespace == "" {
		return false, nil
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return false, nil
	}

	token, err := captureGHCommand("auth", "token", "-u", namespace, "-h", "github.com")
	if err != nil {
		return false, nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false, nil
	}

	loginCmd := exec.Command("docker", "login", "ghcr.io", "-u", namespace, "--password-stdin")
	loginCmd.Stdin = strings.NewReader(token)
	loginCmd.Stdout = stdout
	loginCmd.Stderr = stderr
	if err := loginCmd.Run(); err != nil {
		return false, err
	}
	return true, nil
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
