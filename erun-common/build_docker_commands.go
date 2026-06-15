package eruncommon

import (
	"bytes"
	"errors"
	"fmt"
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
		if err := tagStableBaseVersionAfterBuild(buildInput, platform, stdout, stderr); err != nil {
			return err
		}
	}
	if !buildInput.Push {
		return nil
	}
	return pushMultiPlatformImage(buildInput.Image.Tag, perPlatformTags, buildInput.Verbosity, stdout, stderr)
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
		if err := tagStableBaseVersionAfterBuild(buildInput, platform, stdout, stderr); err != nil {
			return err
		}
	}
	if !buildInput.Push {
		return nil
	}
	return pushMultiPlatformImage(buildInput.Image.Tag, perPlatformTags, buildInput.Verbosity, stdout, stderr)
}

func pushMultiPlatformImage(tag string, perPlatformTags []string, verbosity int, stdout, stderr io.Writer) error {
	for _, platformTag := range perPlatformTags {
		if err := DockerImagePusher(platformTag, verbosity, stdout, stderr); err != nil {
			return err
		}
	}
	createArgs := append([]string{"manifest", "create", "--amend", tag}, perPlatformTags...)
	if err := runDockerSimpleCommandWithVerbosity(createArgs, verbosity, stdout, stderr); err != nil {
		return err
	}
	return runDockerSimpleCommandWithVerbosity([]string{"manifest", "push", tag}, verbosity, stdout, stderr)
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

// tagStableBaseVersionAfterBuild re-tags the platform-suffixed snapshot image
// under the stable BaseVersion tags that `FROM image:${ERUN_VERSION}` wrappers
// resolve from the local daemon (the snapshot tag is never pushed). It writes
// two tags per platform:
//
//   - a per-arch tag, e.g. erun-devops:1.0.90-snapshot-amd64 — what a wrapper
//     resolves for that platform (see dockerBuildArgs), so a multi-platform
//     wrapper build always finds the matching architecture locally.
//   - the arch-less tag, e.g. erun-devops:1.0.90-snapshot, kept for any
//     consumer that references the bare BaseVersion. It is overwritten per
//     platform (last arch wins) and is therefore single-arch, which is exactly
//     why wrappers must use the per-arch tag instead.
//
// Returns nil when BaseVersion is empty (release builds, whose Version equals
// the stable tag — those resolve the base from its pushed multi-arch manifest,
// not from a local tag).
func tagStableBaseVersionAfterBuild(buildInput DockerBuildSpec, platform string, stdout, stderr io.Writer) error {
	target := stableBaseVersionTag(buildInput.Image)
	if target == "" {
		return nil
	}
	sourceTag := platformSuffixedTag(buildInput.Image.Tag, platform)
	if archTarget := platformSuffixedTag(target, platform); archTarget != sourceTag {
		if err := runDockerTag(sourceTag, archTarget, stdout, stderr); err != nil {
			return err
		}
	}
	if sourceTag == target {
		return nil
	}
	return runDockerTag(sourceTag, target, stdout, stderr)
}

// stableBaseVersionTag returns the unsuffixed local tag wrappers reference
// from `FROM image:${ERUN_VERSION}`. Empty when the image has no separate
// BaseVersion (release builds where Version itself is stable) or when the
// computed tag equals the timestamped Tag.
func stableBaseVersionTag(image DockerImageReference) string {
	baseVersion := strings.TrimSpace(image.BaseVersion)
	if baseVersion == "" {
		return ""
	}
	registry := strings.TrimSpace(image.Registry)
	repo := image.ImageName
	if registry != "" {
		repo = strings.TrimRight(registry, "/") + "/" + image.ImageName
	}
	tag := repo + ":" + baseVersion
	if tag == strings.TrimSpace(image.Tag) {
		return ""
	}
	return tag
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

// runDockerSimpleCommandWithVerbosity invokes a docker subcommand that has no
// native `--quiet` flag (manifest create/push, tag) and applies the verbosity
// suppression policy: at VerbosityDebug or higher the subprocess streams to
// stdout/stderr; at VerbosityInfo the output is captured silently and only
// replayed on error so failures stay debuggable.
func runDockerSimpleCommandWithVerbosity(args []string, verbosity int, stdout, stderr io.Writer) error {
	if verbosity >= VerbosityDebug {
		return runDockerSimpleCommand(args, stdout, stderr)
	}
	capture := new(bytes.Buffer)
	if err := runDockerSimpleCommand(args, capture, capture); err != nil {
		if output := strings.TrimSpace(capture.String()); output != "" {
			return fmt.Errorf("%w\n%s", err, output)
		}
		return err
	}
	return nil
}

func runDockerTag(source, target string, stdout, stderr io.Writer) error {
	if err := tryDockerTag(source, target, stdout, stderr); err == nil {
		return nil
	} else if !dockerTagAlreadyExistsRace(err) {
		return err
	}
	// Workaround for the daemon-side race where the target tag is
	// claimed by a stale manifest list (from a previous push, or the
	// previous platform's tag in this same multi-arch run). The
	// daemon reports it as "AlreadyExists: image ... already exists
	// after deleting the existing one". `docker image rm -f` and
	// `docker manifest rm` clear both possible holders; we ignore
	// their errors because the target may legitimately not exist as
	// either kind. Then the retry of `docker tag` succeeds.
	_ = runDockerSimpleCommand([]string{"manifest", "rm", target}, io.Discard, io.Discard)
	_ = runDockerSimpleCommand([]string{"image", "rm", "-f", target}, io.Discard, io.Discard)
	return tryDockerTag(source, target, stdout, stderr)
}

// tryDockerTag runs `docker tag` and captures stderr so the caller can
// pattern-match the daemon error without re-reading the pipe.
func tryDockerTag(source, target string, stdout, stderr io.Writer) error {
	capture := new(bytes.Buffer)
	cmd := Command("docker", "tag", source, target)
	if stdout != nil {
		cmd.Stdout = stdout
	}
	cmd.Stderr = dockerCommandOutputWriter(stderr, capture)
	if err := cmd.Run(); err != nil {
		return dockerTagError{err: err, message: capture.String()}
	}
	return nil
}

type dockerTagError struct {
	err     error
	message string
}

func (e dockerTagError) Error() string {
	msg := strings.TrimSpace(e.message)
	if msg == "" {
		return e.err.Error()
	}
	return fmt.Sprintf("%s: %s", e.err.Error(), msg)
}

func (e dockerTagError) Unwrap() error {
	return e.err
}

func dockerTagAlreadyExistsRace(err error) bool {
	var tagErr dockerTagError
	if !errors.As(err, &tagErr) {
		return false
	}
	return strings.Contains(tagErr.message, "AlreadyExists") ||
		strings.Contains(tagErr.message, "already exists")
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
	args := []string{"build", "--platform", platform, "--provenance=false"}
	args = append(args, dockerVerbosityBuildFlags(buildInput.Verbosity)...)
	args = append(args, "-t", tag)
	buildArgVersion := dockerImageTagVersion(strings.TrimSpace(buildInput.Image.Tag))
	if buildInput.Image.BaseVersion != "" {
		buildArgVersion = buildInput.Image.BaseVersion
		// A wrapper that resolves its base via ${ERUN_VERSION} must point at the
		// base's per-arch stable tag (e.g. erun-devops:1.0.90-snapshot-amd64).
		// The arch-less stable tag names only the last arch built, so a
		// multi-platform wrapper build would otherwise resolve the wrong arch
		// (and, on a strict image store, fail "not found"). The base publishes
		// the matching <BaseVersion>-<arch> tag in tagStableBaseVersionAfterBuild.
		// Only the snapshot path needs this — a release wrapper resolves its base
		// from the base's pushed multi-arch manifest, so BaseVersion is empty and
		// this branch is skipped.
		if dockerfileHasVersionedFrom(buildInput.DockerfilePath) {
			buildArgVersion = buildArgVersion + "-" + platformShortSuffix(platform)
		}
	}
	if buildArgVersion != "" {
		args = append(args, "--build-arg", "ERUN_VERSION="+buildArgVersion)
	}
	args = append(args, "-f", buildInput.DockerfilePath, ".")
	return args
}

func dockerVerbosityBuildFlags(verbosity int) []string {
	if verbosity >= VerbosityDebug {
		return []string{"--progress=plain"}
	}
	return []string{"--quiet"}
}

func dockerPushArgs(tag string, verbosity int) []string {
	if verbosity >= VerbosityDebug {
		return []string{"push", tag}
	}
	return []string{"push", "--quiet", tag}
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

func DockerImagePusher(tag string, verbosity int, stdout, stderr io.Writer) error {
	err := runDockerPushOnce(tag, verbosity, stdout, stderr)
	if err == nil {
		return nil
	}
	if shouldRetryAfterGHCRNamespaceLogin(err, tag, stdout, stderr) {
		if retryErr := runDockerPushOnce(tag, verbosity, stdout, stderr); retryErr == nil {
			return nil
		} else {
			err = retryErr
		}
	}
	return err
}

func runDockerPushOnce(tag string, verbosity int, stdout, stderr io.Writer) error {
	args := dockerPushArgs(tag, verbosity)
	pushCmd := Command("docker", args...)
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
