package eruncommon

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
)

const fingerprintTagPrefix = "fp-"

func DockerImageBuilder(buildInput DockerBuildSpec, stdout, stderr io.Writer) error {
	if buildInput.Promote {
		return promoteDockerImage(buildInput, stdout, stderr)
	}
	return runMultiPlatformBuild(buildInput, stdout, stderr)
}

func runMultiPlatformBuild(buildInput DockerBuildSpec, stdout, stderr io.Writer) error {
	perPlatformTags := make([]string, 0, len(buildInput.Platforms))
	for _, platform := range buildInput.Platforms {
		platformTag := platformSuffixedTag(buildInput.Image.Tag, platform)
		perPlatformTags = append(perPlatformTags, platformTag)
		args := dockerBuildArgs(buildInput, platform)
		if err := runDockerBuildOnce(args, buildInput.ContextDir, buildInput.Image.Tag, false, stdout, stderr); err != nil {
			return err
		}
		if err := tagFingerprintAfterBuild(buildInput, platform, stdout, stderr); err != nil {
			return err
		}
	}
	if !buildInput.Push {
		return nil
	}
	return pushMultiPlatformImage(buildInput.Image.Tag, perPlatformTags, stdout, stderr)
}

func promoteDockerImage(buildInput DockerBuildSpec, stdout, stderr io.Writer) error {
	perPlatformTags := make([]string, 0, len(buildInput.Platforms))
	for _, platform := range buildInput.Platforms {
		fpTag := fingerprintTag(buildInput.Image, buildInput.Fingerprint, platform)
		platformTag := platformSuffixedTag(buildInput.Image.Tag, platform)
		perPlatformTags = append(perPlatformTags, platformTag)
		if err := runDockerTag(fpTag, platformTag, stdout, stderr); err != nil {
			return err
		}
	}
	if !buildInput.Push {
		return nil
	}
	return pushMultiPlatformImage(buildInput.Image.Tag, perPlatformTags, stdout, stderr)
}

func pushMultiPlatformImage(tag string, perPlatformTags []string, stdout, stderr io.Writer) error {
	for _, platformTag := range perPlatformTags {
		if err := DockerImagePusher(platformTag, stdout, stderr); err != nil {
			return err
		}
	}
	createArgs := append([]string{"manifest", "create", "--amend", tag}, perPlatformTags...)
	if err := runDockerSimpleCommand(createArgs, stdout, stderr); err != nil {
		return err
	}
	return runDockerSimpleCommand([]string{"manifest", "push", tag}, stdout, stderr)
}

func tagFingerprintAfterBuild(buildInput DockerBuildSpec, platform string, stdout, stderr io.Writer) error {
	if buildInput.Fingerprint == "" {
		return nil
	}
	sourceTag := platformSuffixedTag(buildInput.Image.Tag, platform)
	target := fingerprintTag(buildInput.Image, buildInput.Fingerprint, platform)
	if sourceTag == target {
		return nil
	}
	return runDockerTag(sourceTag, target, stdout, stderr)
}

func fingerprintTag(image DockerImageReference, fingerprint, platform string) string {
	registry := strings.TrimSpace(image.Registry)
	repo := image.ImageName
	if registry != "" {
		repo = strings.TrimRight(registry, "/") + "/" + image.ImageName
	}
	suffix := fingerprintTagPrefix + fingerprint
	if platform != "" {
		suffix = suffix + "-" + platformShortSuffix(platform)
	}
	return repo + ":" + suffix
}

func platformSuffixedTag(tag, platform string) string {
	return tag + "-" + platformShortSuffix(platform)
}

func platformShortSuffix(platform string) string {
	parts := strings.SplitN(platform, "/", 2)
	if len(parts) == 2 && parts[1] != "" {
		return parts[1]
	}
	return strings.ReplaceAll(platform, "/", "-")
}

func runDockerBuildOnce(args []string, dir, authContextTag string, push bool, stdout, stderr io.Writer) error {
	cmd := Command("docker", args...)
	cmd.Dir = dir
	output := new(bytes.Buffer)
	cmd.Stdout = dockerCommandOutputWriter(stdout, output)
	cmd.Stderr = dockerCommandOutputWriter(stderr, output)
	err := cmd.Run()
	if err == nil {
		return nil
	}

	message := output.String()
	if push && IsDockerPushAuthorizationError(message) {
		return DockerRegistryAuthError{
			Tag:      authContextTag,
			Registry: dockerRegistryFromImageTag(authContextTag),
			Message:  strings.TrimSpace(message),
			Err:      err,
		}
	}
	return err
}

func runDockerSimpleCommand(args []string, stdout, stderr io.Writer) error {
	cmd := Command("docker", args...)
	if stdout != nil {
		cmd.Stdout = stdout
	}
	if stderr != nil {
		cmd.Stderr = stderr
	}
	return cmd.Run()
}

func runDockerTag(source, target string, stdout, stderr io.Writer) error {
	return runDockerSimpleCommand([]string{"tag", source, target}, stdout, stderr)
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
	cmd := Command("docker", "image", "inspect", tag)
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
	cmd := Command("docker", "manifest", "inspect", tag)
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
func dockerManifestPlatforms(ctx Context, tag string) ([]string, error) {
	ctx.TraceCommand("", "docker", "manifest", "inspect", tag)
	cmd := Command("docker", "manifest", "inspect", tag)
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

// dockerLocalImagePlatforms returns the platforms covered by the local Docker
// image store for the given tag. With the traditional image store, a tag holds
// one platform; with the containerd image store, the inspect output's Manifests
// field reports every platform stored under that tag. Returns nil when the
// image is not present locally.
func dockerLocalImagePlatforms(ctx Context, tag string) ([]string, error) {
	ctx.TraceCommand("", "docker", "image", "inspect", tag)
	cmd := Command("docker", "image", "inspect", tag)
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, nil // image absent
		}
		return nil, err
	}
	var entries []struct {
		Os           string `json:"Os"`
		Architecture string `json:"Architecture"`
		Manifests    []struct {
			ImageData struct {
				Platform struct {
					OS           string `json:"os"`
					Architecture string `json:"architecture"`
				} `json:"Platform"`
			} `json:"ImageData"`
		} `json:"Manifests"`
	}
	if err := json.Unmarshal(output, &entries); err != nil || len(entries) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, 4)
	platforms := make([]string, 0, 4)
	add := func(os, arch string) {
		if os == "" || arch == "" {
			return
		}
		platform := os + "/" + arch
		if _, ok := seen[platform]; ok {
			return
		}
		seen[platform] = struct{}{}
		platforms = append(platforms, platform)
	}
	for _, entry := range entries {
		for _, m := range entry.Manifests {
			add(m.ImageData.Platform.OS, m.ImageData.Platform.Architecture)
		}
		if len(entry.Manifests) == 0 {
			add(entry.Os, entry.Architecture)
		}
	}
	if len(platforms) == 0 {
		return nil, nil
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

func dockerBuildArgs(buildInput DockerBuildSpec, platform string) []string {
	tag := platformSuffixedTag(strings.TrimSpace(buildInput.Image.Tag), platform)
	// BuildKit's default exporter wraps each per-platform image in an OCI
	// index alongside a provenance attestation manifest, which makes the
	// resulting tag a manifest list. `docker manifest create` rejects
	// manifest-list inputs ("<tag> is a manifest list"), so provenance stays
	// off and each per-arch tag is a plain image manifest the assembly step
	// can consume.
	args := []string{"build", "--platform", platform, "--provenance=false", "-t", tag}
	buildArgVersion := dockerImageTagVersion(strings.TrimSpace(buildInput.Image.Tag))
	if buildInput.Image.BaseVersion != "" {
		buildArgVersion = buildInput.Image.BaseVersion
	}
	if buildArgVersion != "" {
		args = append(args, "--build-arg", "ERUN_VERSION="+buildArgVersion)
	}
	args = append(args, "-f", buildInput.DockerfilePath, ".")
	return args
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
	cmd := Command(scriptPath)
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
	pushCmd := Command("docker", "push", tag)
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

	loginCmd := Command("docker", args...)
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

	loginCmd := Command("docker", "login", "ghcr.io", "-u", user, "--password-stdin")
	loginCmd.Stdin = strings.NewReader(token)
	loginCmd.Stdout = stdout
	loginCmd.Stderr = stderr
	if err := loginCmd.Run(); err != nil {
		return false, err
	}
	return true, nil
}

func captureGHCommand(args ...string) (string, error) {
	cmd := Command("gh", args...)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// RefreshGHCRPackageScopes widens the gh-stored token's scopes to include
// write:packages,read:packages and then re-runs TryGHCRNamespaceLogin so
// docker is authed with the freshly-scoped token.
//
// `gh auth refresh` operates on the currently active gh account and does
// not accept a `-u` flag, so this helper first runs `gh auth switch -u
// <namespace>` best-effort to make the namespace owner active. If switch
// fails (single-account install, account not logged in), the refresh runs
// against whichever account is active.
//
// The refresh flow is interactive (browser device-code), so stdin must be
// a real terminal. Returns (true, nil) when the refresh completed and
// docker login was redone, (false, nil) when prerequisites are missing
// (gh not installed, non-ghcr tag, missing namespace), and (false, err)
// for gh or docker errors.
//
// Use this only after TryGHCRNamespaceLogin + retry fails with a
// scope-denied error: that signals the gh-stored token itself lacks the
// scope, and the only remedy is replacing the token.
func RefreshGHCRPackageScopes(tag string, stdin io.Reader, stdout, stderr io.Writer) (bool, error) {
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

	switchCmd := Command("gh", "auth", "switch", "-h", "github.com", "-u", namespace)
	switchCmd.Stdout = stdout
	switchCmd.Stderr = stderr
	switchErr := switchCmd.Run()

	var ghCmd *exec.Cmd
	if switchErr == nil {
		// Namespace owner is already logged in and is now the active
		// account; widen its scopes.
		ghCmd = Command("gh", "auth", "refresh", "-h", "github.com", "-s", "write:packages,read:packages")
	} else {
		// Namespace owner is not logged in. Add it via the interactive
		// browser flow, requesting the package scopes up front.
		ghCmd = Command("gh", "auth", "login", "-h", "github.com", "-s", "write:packages,read:packages", "-w", "--git-protocol", "https")
	}
	ghCmd.Stdin = stdin
	ghCmd.Stdout = stdout
	ghCmd.Stderr = stderr
	if err := ghCmd.Run(); err != nil {
		return false, err
	}

	return TryGHCRNamespaceLogin(tag, stdout, stderr)
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

	loginCmd := Command("docker", "login", "ghcr.io", "-u", namespace, "--password-stdin")
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
