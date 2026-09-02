package eruncommon

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const fingerprintTagPrefix = "fp-"

func DockerImageBuilder(buildInput DockerBuildSpec, stdout, stderr io.Writer) error {
	if buildInput.Promote {
		return promoteDockerImage(buildInput, stdout, stderr)
	}
	return runMultiPlatformBuild(buildInput, stdout, stderr)
}

func runMultiPlatformBuild(buildInput DockerBuildSpec, stdout, stderr io.Writer) error {
	if err := verifyDockerBuildPlatforms(buildInput.Platforms); err != nil {
		return err
	}
	warnAboutBridgeMTUMismatch(stderr)
	perPlatformTags := make([]string, 0, len(buildInput.Platforms))
	for _, platform := range buildInput.Platforms {
		started := time.Now()
		platformTag := platformSuffixedTag(buildInput.Image.Tag, platform)
		perPlatformTags = append(perPlatformTags, platformTag)
		err := buildPlatformImageFromSource(buildInput, platform, stdout, stderr)
		if buildInput.PlatformObserver != nil {
			buildInput.PlatformObserver(platform, time.Since(started), err)
		}
		if err != nil {
			return err
		}
	}
	if !buildInput.Push {
		return nil
	}
	return assembleMultiPlatformManifest(buildInput.Image.Tag, perPlatformTags, buildInput.Image.Insecure, buildInput.Verbosity, stdout, stderr)
}

// buildPlatformImageFromSource runs one platform's real docker build, tags
// its fingerprint and stable-base aliases, and pushes it. It is also the
// fallback promotePlatformImage reaches for when the registry rejects a
// promoted tag, so a cache-hit decision that turns out to be wrong at push
// time still ends in a real, correctly-tagged image rather than a failure.
func buildPlatformImageFromSource(buildInput DockerBuildSpec, platform string, stdout, stderr io.Writer) error {
	platformTag := platformSuffixedTag(buildInput.Image.Tag, platform)
	args := dockerBuildArgs(buildInput, platform)
	err := runDockerBuildOnce(args, buildInput.ContextDir, buildInput.Image.Tag, false, buildInput.Verbosity, stdout, stderr)
	if err == nil {
		err = tagFingerprintAfterBuild(buildInput, platform, stdout, stderr)
	}
	if err == nil {
		err = tagStableBaseVersionAfterBuild(buildInput, platform, stdout, stderr)
	}
	if err == nil {
		err = pushPlatformImage(buildInput, platformTag, stdout, stderr)
	}
	return err
}

func promoteDockerImage(buildInput DockerBuildSpec, stdout, stderr io.Writer) error {
	perPlatformTags := make([]string, 0, len(buildInput.Platforms))
	for _, platform := range buildInput.Platforms {
		started := time.Now()
		platformTag := platformSuffixedTag(buildInput.Image.Tag, platform)
		perPlatformTags = append(perPlatformTags, platformTag)
		err := promotePlatformImage(buildInput, platform, stdout, stderr)
		if buildInput.PlatformObserver != nil {
			buildInput.PlatformObserver(platform, time.Since(started), err)
		}
		if err != nil {
			return err
		}
	}
	if !buildInput.Push {
		return nil
	}
	return assembleMultiPlatformManifest(buildInput.Image.Tag, perPlatformTags, buildInput.Image.Insecure, buildInput.Verbosity, stdout, stderr)
}

// promotePlatformImage re-tags one platform's cached fingerprint image and
// pushes it under the real version tag. The fingerprint check that chose this
// path only proves the image exists in the local daemon; it says nothing
// about whether the registry still holds every blob that image references.
// A push the registry rejects for a blob it doesn't have — surfacing as
// "unknown blob" — means the cache hit cannot be trusted for this run, so
// promotion is only ever an optimization over building from source: a
// rejection here falls back to building and pushing this platform for real,
// rather than failing the whole release over a check that was wrong.
//
// Any other failure (a real auth or network error, for instance) is not
// retried, since rebuilding could not change its outcome; it is returned with
// the promoted tag and its cached source named, so the failure says which
// image and which operation it belongs to instead of a bare daemon message.
func promotePlatformImage(buildInput DockerBuildSpec, platform string, stdout, stderr io.Writer) error {
	fpTag := fingerprintTag(buildInput.Image, buildInput.Fingerprint, platform)
	platformTag := platformSuffixedTag(buildInput.Image.Tag, platform)
	err := runDockerTag(fpTag, platformTag, stdout, stderr)
	if err == nil {
		err = tagStableBaseVersionAfterBuild(buildInput, platform, stdout, stderr)
	}
	if err == nil {
		err = pushPlatformImage(buildInput, platformTag, stdout, stderr)
	}
	if err == nil {
		return nil
	}
	if !IsDockerUnknownBlobError(err.Error()) {
		return fmt.Errorf("promote %s from cached fingerprint image %s: %w", platformTag, fpTag, err)
	}
	fmt.Fprintf(stderr, "==> promoting %s from cached fingerprint image %s failed (%v); the registry does not have every blob it references, so rebuilding from source instead of trusting the cache\n", platformTag, fpTag, err)
	return buildPlatformImageFromSource(buildInput, platform, stdout, stderr)
}

// pushPlatformImage publishes one platform the moment it is built, instead of
// leaving every platform for a push pass that runs after the last build.
//
// A daemon backed by the containerd image store is free to collect content that
// only a tag points at, and does. With build-all-then-push the first platform's
// manifest was routinely gone by the time its push ran, and the release failed
// with "content digest ... not found" — after paying for every build. Publishing
// each platform immediately means no artifact ever has to survive another build,
// which is the property that was missing rather than anything about the content.
//
// The manifest list is unaffected: `docker manifest create` reads its inputs
// back from the registry, so it does not care whether they are still local.
func pushPlatformImage(buildInput DockerBuildSpec, platformTag string, stdout, stderr io.Writer) error {
	if !buildInput.Push {
		return nil
	}
	return DockerImagePusher(platformTag, buildInput.Verbosity, stdout, stderr)
}

// assembleMultiPlatformManifest publishes the arch-less tag that points at the
// per-arch images just pushed.
//
// The local manifest list is discarded first. docker keeps manifest lists in its
// own store, so re-publishing a version that was pushed before would otherwise
// merge into the cached list and republish the digests it already held: the
// per-arch tags advance, the arch-less tag does not, and a deploy of that version
// runs the previous image while every step reports success. Removing the cached
// list makes the published tag a function of this run alone. It is absent on a
// first publish, so its failure is not one.
func assembleMultiPlatformManifest(tag string, perPlatformTags []string, insecure bool, verbosity int, stdout, stderr io.Writer) error {
	// `docker manifest rm` only touches the local manifest-list store, never the
	// registry, so it needs no --insecure of its own.
	_ = runDockerSimpleCommandWithVerbosity([]string{"manifest", "rm", tag}, verbosity, io.Discard, io.Discard)
	createArgs := append(dockerManifestArgs("create", insecure), tag)
	createArgs = append(createArgs, perPlatformTags...)
	if err := runDockerSimpleCommandWithVerbosity(createArgs, verbosity, stdout, stderr); err != nil {
		return err
	}
	pushArgs := append(dockerManifestArgs("push", insecure), tag)
	return runDockerSimpleCommandWithVerbosity(pushArgs, verbosity, stdout, stderr)
}

// dockerManifestArgs builds a `docker manifest <sub>` argv, appending
// --insecure when the registry is plain HTTP. Unlike the daemon (which reads
// its own insecure-registry list), `docker manifest create/push/inspect` talk
// to the registry directly over HTTPS by default and need the flag spelled
// out on every invocation that touches an insecure registry.
func dockerManifestArgs(sub string, insecure bool) []string {
	args := []string{"manifest", sub}
	if insecure {
		args = append(args, "--insecure")
	}
	return args
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

// tagStableBaseVersionAfterBuild makes a snapshot base resolvable by wrappers
// whose `FROM image:${ERUN_VERSION}` reads it from the local daemon (the
// snapshot tag is never pushed). The per-arch tag is what a multi-platform
// wrapper build must resolve, because the arch-less tag is last-arch-wins and
// therefore single-arch. No-op for release builds, whose base resolves from its
// pushed multi-arch manifest rather than a local tag.
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

// stableBaseVersionTag returns the unsuffixed local tag a wrapper resolves from
// `FROM image:${ERUN_VERSION}`.
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

// runDockerBuildOnce always builds with --progress=plain (see
// dockerVerbosityBuildFlags) so BuildKit emits every step's own output,
// including a failing RUN's — the in-Dockerfile `make check` test stage is
// exactly such a step. Below debug verbosity that output is captured rather
// than streamed live, so a successful build stays as quiet as --quiet used to
// make it; on failure the capture is flushed to stderr before the error
// returns, so "exit code: N" is never the whole story for a step that just
// spent minutes running. At debug verbosity the caller already wants
// everything live, so it streams as it always has.
func runDockerBuildOnce(args []string, dir, authContextTag string, push bool, verbosity int, stdout, stderr io.Writer) error {
	cmd := Command("docker", args...)
	cmd.Dir = dir
	output := new(bytes.Buffer)
	if verbosity >= VerbosityDebug {
		cmd.Stdout = commandOutputWriter(stdout, output)
		cmd.Stderr = commandOutputWriter(stderr, output)
	} else {
		cmd.Stdout = output
		cmd.Stderr = output
	}
	err := cmd.Run()
	if err == nil {
		return nil
	}

	message := output.String()
	if verbosity < VerbosityDebug && stderr != nil {
		_, _ = io.WriteString(stderr, message)
	}
	if push && IsDockerPushAuthorizationError(message) {
		return DockerRegistryAuthError{
			Tag:      authContextTag,
			Registry: dockerRegistryFromImageTag(authContextTag),
			Message:  strings.TrimSpace(message),
			Err:      err,
		}
	}
	if diagnosis, ok := dockerBuildResourceExhaustionDiagnosis(message); ok {
		return DockerBuildResourceExhaustionError{Diagnosis: diagnosis, Err: err}
	}
	// Keep the step's own last words whatever else is known: they are all the
	// durable timing record will ever have (see build_failure_reason.go).
	reason := dockerBuildFailureReason(message)
	if diagnosis, ok := dockerBuildNetworkDiagnosis(message); ok {
		reason = joinFailureReason(reason, diagnosis)
	}
	if reason != "" {
		return DockerBuildStepError{Reason: reason, Err: err}
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

// runDockerSimpleCommandWithVerbosity emulates --quiet for docker subcommands
// (manifest create/push, tag) that lack the native flag, replaying captured
// output only on error.
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
	// Recover from the daemon race where the target tag is held by a stale
	// manifest list (a previous push, or the previous platform in this same
	// multi-arch run), surfacing as "AlreadyExists ... after deleting the
	// existing one". Clearing both possible holders may fail harmlessly when the
	// target exists as neither kind, so their errors are ignored.
	_ = runDockerSimpleCommand([]string{"manifest", "rm", target}, io.Discard, io.Discard)
	_ = runDockerSimpleCommand([]string{"image", "rm", "-f", target}, io.Discard, io.Discard)
	return tryDockerTag(source, target, stdout, stderr)
}

func tryDockerTag(source, target string, stdout, stderr io.Writer) error {
	capture := new(bytes.Buffer)
	cmd := Command("docker", "tag", source, target)
	if stdout != nil {
		cmd.Stdout = stdout
	}
	cmd.Stderr = commandOutputWriter(stderr, capture)
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

func DockerManifestExists(tag string, insecure bool) (bool, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return false, nil
	}
	args := append(dockerManifestArgs("inspect", insecure), tag)
	cmd := Command("docker", args...)
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

func commandOutputWriter(primary io.Writer, capture io.Writer) io.Writer {
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
	// --provenance=false: BuildKit's default provenance attestation turns each
	// per-arch tag into a manifest list, which `docker manifest create` rejects
	// ("<tag> is a manifest list"). Off, each tag stays a plain image manifest
	// the assembly step can consume.
	args := []string{"build", "--platform", platform, "--provenance=false"}
	// Always plain progress, never --quiet: --quiet suppresses a failing step's
	// own output at the source (BuildKit, not erun), so no amount of
	// capture-and-replay in runDockerBuildOnce could recover what --quiet never
	// produced. runDockerBuildOnce is what keeps a successful build quiet below
	// debug verbosity; this flag only has to make the output exist to capture.
	args = append(args, "--progress=plain")
	args = append(args, "-t", tag)
	buildArgVersion := dockerBuildArgVersion(buildInput)
	// A base this run keeps local — a snapshot base, or a pinned-version base built
	// without pushing — is only resolvable from the daemon, and only under its
	// per-arch tags: the arch-less tag names just the last arch built, so a
	// multi-platform wrapper would otherwise pull the wrong arch (or fail "not
	// found" on a strict image store). Release wrappers keep the plain version;
	// they resolve their base from its pushed multi-arch manifest.
	baseIsLocal := strings.TrimSpace(buildInput.LocalBaseTag) != "" || buildInput.Image.BaseVersion != ""
	if baseIsLocal && buildArgVersion != "" && dockerfileHasVersionedFrom(buildInput.DockerfilePath) {
		buildArgVersion = buildArgVersion + "-" + platformShortSuffix(platform)
	}
	if buildArgVersion != "" {
		args = append(args, "--build-arg", "ERUN_VERSION="+buildArgVersion)
	}
	args = append(args, "-f", buildInput.DockerfilePath, ".")
	return args
}

// dockerBuildArgVersion is the value the ERUN_VERSION build arg carries before
// any per-platform suffix: the stable base version when the build resolves a
// base locally, else the version in the image's own tag. Shared with the
// fingerprint so the identity a build is cached under and the version that build
// would bake in can never disagree.
func dockerBuildArgVersion(buildInput DockerBuildSpec) string {
	if base := strings.TrimSpace(buildInput.Image.BaseVersion); base != "" {
		return base
	}
	return dockerImageTagVersion(strings.TrimSpace(buildInput.Image.Tag))
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
	// Project build scripts are POSIX shell; Windows can't exec a shebang script
	// by name ("%1 is not a valid Win32 application"), so run it through sh.
	cmd := Command(scriptPath)
	if runtime.GOOS == "windows" {
		cmd = Command("sh", scriptPath)
	}
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
	pushCmd.Stdout = commandOutputWriter(stdout, output)
	pushCmd.Stderr = commandOutputWriter(stderr, output)
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
	return dockerPushError{tag: tag, err: err, message: message}
}

// dockerPushError names the tag a push was attempting when the daemon or
// registry rejected it, so a bare registry response like "unknown blob" does
// not surface with nothing to say which image, or which command, it belongs
// to.
type dockerPushError struct {
	tag     string
	err     error
	message string
}

func (e dockerPushError) Error() string {
	msg := strings.TrimSpace(e.message)
	if msg == "" {
		return fmt.Sprintf("docker push %s: %s", e.tag, e.err.Error())
	}
	return fmt.Sprintf("docker push %s: %s: %s", e.tag, e.err.Error(), msg)
}

func (e dockerPushError) Unwrap() error {
	return e.err
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

func isHostedRegistry(registry string) bool {
	return strings.EqualFold(strings.TrimSpace(registry), HostedRegistryHost)
}

// DockerRegistryLoginWithHostedRegistry wraps DockerRegistryLogin with the one
// branch it cannot resolve on its own: the hosted registry's password is the
// operator's own erun-api bearer token (see the registry token endpoint in
// api-protocol.md), minted from their configured erun cloud provider alias —
// never a secret an operator could type by hand. Every other registry falls
// through to DockerRegistryLogin unchanged.
func DockerRegistryLoginWithHostedRegistry(store CloudReadStore, deps CloudDependencies) DockerRegistryLoginFunc {
	return func(registry string, stdin io.Reader, stdout, stderr io.Writer) error {
		if isHostedRegistry(registry) {
			return hostedRegistryDockerLogin(store, deps, stdout, stderr)
		}
		return DockerRegistryLogin(registry, stdin, stdout, stderr)
	}
}

// hostedRegistryDockerLogin resolves the operator's sole configured erun
// platform cloud provider alias, mints a fresh bearer token from it, and feeds
// that token to `docker login` as the password over stdin — never argv, so it
// never appears in a process listing.
func hostedRegistryDockerLogin(store CloudReadStore, deps CloudDependencies, stdout, stderr io.Writer) error {
	provider, err := ResolveERunPlatformAlias(store, "")
	if err != nil {
		return fmt.Errorf("erun's hosted registry authenticates with the tenant's own erun-api bearer token, minted from a configured erun cloud provider alias: %w", err)
	}
	token, err := CloudProviderBearerToken(Context{}, store, CloudBearerParams{Alias: provider.Alias}, deps)
	if err != nil {
		return fmt.Errorf("mint erun-api bearer token for hosted registry login: %w", err)
	}
	if strings.TrimSpace(token.Token) == "" {
		return fmt.Errorf("erun cloud provider alias %q returned an empty bearer token", provider.Alias)
	}
	loginCmd := Command("docker", "login", HostedRegistryHost, "-u", HostedRegistryLoginUsername, "--password-stdin")
	loginCmd.Stdin = strings.NewReader(token.Token)
	loginCmd.Stdout = stdout
	loginCmd.Stderr = stderr
	return loginCmd.Run()
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

// RefreshGHCRPackageScopes widens the gh-stored token to write:packages so a
// push denied for lacking that scope can succeed. Call it only after
// TryGHCRNamespaceLogin + retry still fails scope-denied: that means the token
// itself lacks the scope and must be re-minted.
//
// `gh auth refresh` has no `-u` flag and acts on the active account, so this
// switches to the namespace owner first (best-effort; else runs against the
// active account). The refresh drives gh's interactive browser device-code
// flow, so it bails with an actionable recovery error rather than launching gh
// in a headless pod or any non-interactive context (MCP, CI, pipes) where the
// prompt could never advance.
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

	// Bail before mutating any gh state: the switch/refresh/login below all drive
	// gh's interactive browser flow, which hangs forever in a headless pod or any
	// non-interactive context with no operator at the prompt.
	if !interactiveGHAuthAllowed(stdin) {
		return false, newNonInteractiveGHCRScopeRefreshError(tag, namespace)
	}

	switchCmd := Command("gh", "auth", "switch", "-h", "github.com", "-u", namespace)
	switchCmd.Stdout = stdout
	switchCmd.Stderr = stderr
	switchErr := switchCmd.Run()

	var ghCmd *exec.Cmd
	if switchErr == nil {
		ghCmd = Command("gh", "auth", "refresh", "-h", "github.com", "-s", "write:packages,read:packages")
	} else {
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

// interactiveGHAuthAllowed reports whether gh's interactive browser device-code
// flow can complete here. The in-pod check comes first because a runtime pod
// shell is PTY-backed and would pass the terminal test, yet has no browser to
// finish the device flow; otherwise stdin must be a real terminal (not MCP, CI,
// or a pipe). ERUN_FORCE_TTY=1 is the CLI's test seam so the integration
// harness, whose stdin is always a pipe, can still exercise the interactive path.
func interactiveGHAuthAllowed(stdin io.Reader) bool {
	if inInjectedRuntimePod() {
		return false
	}
	if os.Getenv("ERUN_FORCE_TTY") == "1" {
		return true
	}
	file, ok := stdin.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}

// inInjectedRuntimePod detects the chart-injected runtime pod.
func inInjectedRuntimePod() bool {
	_, _, ok := injectedRuntimePodIdentity(os.Getenv)
	return ok
}

func newNonInteractiveGHCRScopeRefreshError(tag, namespace string) error {
	registry := dockerRegistryFromImageTag(tag)
	if registry == "" {
		registry = "ghcr.io"
	}
	reason := "stdin is not an interactive terminal"
	if inInjectedRuntimePod() {
		reason = "this runtime pod is headless and has no browser"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s rejected the push: the gh-stored token lacks the write:packages scope.\n\n", registry)
	fmt.Fprintf(&sb, "erun can widen the scope automatically, but that needs gh's interactive browser login, which cannot run here (%s).\n", reason)
	sb.WriteString("From a host shell signed in to the namespace owner's GitHub account, with a browser available, run:\n")
	fmt.Fprintf(&sb, "  gh auth refresh -h github.com -u %s -s write:packages,read:packages\n", namespace)
	fmt.Fprintf(&sb, "  gh auth token -u %s -h github.com | docker login %s -u %s --password-stdin\n", namespace, registry, namespace)
	sb.WriteString("then re-run erun push.")
	return errors.New(sb.String())
}

// TryGHCRNamespaceLogin re-authenticates docker to ghcr.io as the gh-configured
// user that owns the target namespace. Call it after a push fails with
// IsDockerCreatePackageDenied, to try an automatic auth switch before falling
// back to an interactive prompt.
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
