package build

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	registrypkg "github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func DockerImageBuilder(buildInput shared.DockerBuildSpec, stdout, stderr io.Writer) error {
	if buildInput.Promote {
		return promoteDockerImage(buildInput, stdout, stderr)
	}
	return runMultiPlatformBuild(buildInput, stdout, stderr)
}

func runMultiPlatformBuild(buildInput shared.DockerBuildSpec, stdout, stderr io.Writer) error {
	if err := verifyDockerBuildPlatforms(buildInput.Platforms); err != nil {
		return err
	}
	warnAboutBridgeMTUMismatch(stderr)
	perPlatformTags := make([]string, 0, len(buildInput.Platforms))
	for _, platform := range buildInput.Platforms {
		started := time.Now()
		cgroupBefore := core.CaptureBuildCgroupSnapshot()
		platformTag := shared.PlatformSuffixedTag(buildInput.Image.Tag, platform)
		perPlatformTags = append(perPlatformTags, platformTag)
		output, err := buildPlatformImageFromSource(buildInput, platform, stdout, stderr)
		elapsed := time.Since(started)
		if buildInput.PlatformObserver != nil {
			cgroup := core.BuildCgroupMetricsFromSnapshots(cgroupBefore, core.CaptureBuildCgroupSnapshot(), elapsed)
			buildInput.PlatformObserver(platform, elapsed, err, cgroup, output)
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

// buildPlatformImageFromSource builds, tags and pushes one platform, returning the
// captured build output for step timing.
func buildPlatformImageFromSource(buildInput shared.DockerBuildSpec, platform string, stdout, stderr io.Writer) (string, error) {
	platformTag := shared.PlatformSuffixedTag(buildInput.Image.Tag, platform)
	args := shared.DockerBuildArgs(buildInput, platform)
	output, err := runDockerBuildOnce(args, buildInput.ContextDir, buildInput.Image.Tag, false, buildInput.Verbosity, stdout, stderr)
	if err == nil {
		err = tagFingerprintAfterBuild(buildInput, platform, stdout, stderr)
	}
	if err == nil {
		err = tagStableBaseVersionAfterBuild(buildInput, platform, stdout, stderr)
	}
	if err == nil {
		err = pushPlatformImage(buildInput, platformTag, stdout, stderr)
	}
	return output, err
}

func promoteDockerImage(buildInput shared.DockerBuildSpec, stdout, stderr io.Writer) error {
	perPlatformTags := make([]string, 0, len(buildInput.Platforms))
	for _, platform := range buildInput.Platforms {
		started := time.Now()
		platformTag := shared.PlatformSuffixedTag(buildInput.Image.Tag, platform)
		perPlatformTags = append(perPlatformTags, platformTag)
		output, err := promotePlatformImage(buildInput, platform, stdout, stderr)
		if buildInput.PlatformObserver != nil {
			// Promotion never runs `docker build`, so there is no build cost to attribute.
			buildInput.PlatformObserver(platform, time.Since(started), err, nil, output)
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

// promotePlatformImage re-tags and pushes one platform's cached fingerprint image,
// falling back to a real build when settlePromoteFailure deems the cache untrustworthy.
func promotePlatformImage(buildInput shared.DockerBuildSpec, platform string, stdout, stderr io.Writer) (string, error) {
	fpTag := shared.FingerprintTag(buildInput.Image, buildInput.Fingerprint, platform)
	platformTag := shared.PlatformSuffixedTag(buildInput.Image.Tag, platform)
	err := runDockerTag(fpTag, platformTag, stdout, stderr)
	if err == nil {
		err = tagStableBaseVersionAfterBuild(buildInput, platform, stdout, stderr)
	}
	singlePlatform := true
	if err == nil {
		// A promoted tag has no build behind it, so check it is not a multi-platform index,
		// which manifest assembly rejects.
		singlePlatform = localImageIsSinglePlatform(platformTag)
	}
	if err == nil && singlePlatform {
		err = pushPlatformImage(buildInput, platformTag, stdout, stderr)
	}
	if err == nil && singlePlatform {
		return "", nil
	}
	return settlePromoteFailure(buildInput, platform, platformTag, fpTag, err, stdout, stderr)
}

// promoteSourceMissing reports only an affirmative "no image"; a failed inspect is not evidence.
func promoteSourceMissing(fpTag string) bool {
	present, err := shared.DockerImageExists(fpTag)
	return err == nil && !present
}

// settlePromoteFailure rebuilds from source when the cached image vanished, the registry
// reports "unknown blob", or the image is not single-platform; other failures are returned.
func settlePromoteFailure(buildInput shared.DockerBuildSpec, platform, platformTag, fpTag string, promoteErr error, stdout, stderr io.Writer) (string, error) {
	switch {
	case promoteErr == nil:
		_, _ = fmt.Fprintf(stderr, "==> promoting %s from cached fingerprint image %s would publish a multi-platform image under a per-arch tag; rebuilding %s from source instead of trusting the cache\n", platformTag, fpTag, platform)
	case promoteSourceMissing(fpTag):
		_, _ = fmt.Fprintf(stderr, "==> promoting %s from cached fingerprint image %s failed (%v); that cached image is no longer in the local image store, so rebuilding %s from source instead of trusting the cache\n", platformTag, fpTag, promoteErr, platform)
	case shared.IsDockerUnknownBlobError(promoteErr.Error()):
		_, _ = fmt.Fprintf(stderr, "==> promoting %s from cached fingerprint image %s failed (%v); the registry does not have every blob it references, so rebuilding from source instead of trusting the cache\n", platformTag, fpTag, promoteErr)
	default:
		return "", fmt.Errorf("promote %s from cached fingerprint image %s: %w", platformTag, fpTag, promoteErr)
	}
	// The run now does real build work, so the timing row must not report a cache hit.
	buildInput.Cache.NotePromoteFallback()
	return buildPlatformImageFromSource(buildInput, platform, stdout, stderr)
}

// localImageIsSinglePlatform reports false only for a readable multi-platform index
// descriptor; anything unreadable counts as single-platform.
func localImageIsSinglePlatform(tag string) bool {
	cmd := core.Command("docker", "image", "inspect", tag)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return true
	}
	var images []struct {
		Descriptor struct {
			MediaType string `json:"mediaType"`
		} `json:"Descriptor"`
	}
	if err := json.Unmarshal(output.Bytes(), &images); err != nil {
		return true
	}
	for _, image := range images {
		if isImageIndexMediaType(image.Descriptor.MediaType) {
			return false
		}
	}
	return true
}

func isImageIndexMediaType(mediaType string) bool {
	switch strings.TrimSpace(mediaType) {
	case "application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.index.v1+json":
		return true
	default:
		return false
	}
}

// pushPlatformImage publishes each platform as soon as it builds: the containerd store
// may collect tag-only content before a later push pass runs.
func pushPlatformImage(buildInput shared.DockerBuildSpec, platformTag string, stdout, stderr io.Writer) error {
	if !buildInput.Push {
		return nil
	}
	return shared.DockerImagePusher(platformTag, buildInput.Verbosity, stdout, stderr)
}

// assembleMultiPlatformManifest publishes the arch-less tag. The cached local manifest
// list is removed first, or a re-publish would merge into it and keep stale digests.
func assembleMultiPlatformManifest(tag string, perPlatformTags []string, insecure bool, verbosity int, stdout, stderr io.Writer) error {
	// `docker manifest rm` is local-only, so it needs no --insecure.
	_ = runDockerSimpleCommandWithVerbosity([]string{"manifest", "rm", tag}, verbosity, io.Discard, io.Discard)
	createArgs := append(shared.DockerManifestArgs("create", insecure), tag)
	createArgs = append(createArgs, perPlatformTags...)
	if err := runDockerSimpleCommandWithVerbosity(createArgs, verbosity, stdout, stderr); err != nil {
		return err
	}
	pushArgs := append(shared.DockerManifestArgs("push", insecure), tag)
	return runDockerSimpleCommandWithVerbosity(pushArgs, verbosity, stdout, stderr)
}

func tagFingerprintAfterBuild(buildInput shared.DockerBuildSpec, platform string, stdout, stderr io.Writer) error {
	if buildInput.Fingerprint == "" {
		return nil
	}
	sourceTag := shared.PlatformSuffixedTag(buildInput.Image.Tag, platform)
	target := shared.FingerprintTag(buildInput.Image, buildInput.Fingerprint, platform)
	if sourceTag == target {
		return nil
	}
	return runDockerTag(sourceTag, target, stdout, stderr)
}

// tagStableBaseVersionAfterBuild tags a snapshot base locally per arch so wrapper builds
// can resolve it; no-op for release builds.
func tagStableBaseVersionAfterBuild(buildInput shared.DockerBuildSpec, platform string, stdout, stderr io.Writer) error {
	target := shared.StableBaseVersionTag(buildInput.Image)
	if target == "" {
		return nil
	}
	sourceTag := shared.PlatformSuffixedTag(buildInput.Image.Tag, platform)
	if archTarget := shared.PlatformSuffixedTag(target, platform); archTarget != sourceTag {
		if err := runDockerTag(sourceTag, archTarget, stdout, stderr); err != nil {
			return err
		}
	}
	if sourceTag == target {
		return nil
	}
	return runDockerTag(sourceTag, target, stdout, stderr)
}

// runDockerBuildOnce captures --progress=plain output below debug verbosity and flushes
// it on failure; it returns the captured output for step timing.
func runDockerBuildOnce(args []string, dir, authContextTag string, push bool, verbosity int, stdout, stderr io.Writer) (string, error) {
	cmd := core.Command("docker", args...)
	cmd.Dir = dir
	capture := &core.CommandOutputCapture{}
	if verbosity >= core.VerbosityDebug {
		cmd.Stdout = core.TeeWriter(stdout, &capture.Stdout)
		cmd.Stderr = core.TeeWriter(stderr, &capture.Stderr)
	} else {
		cmd.Stdout = &capture.Stdout
		cmd.Stderr = &capture.Stderr
	}
	err := cmd.Run()
	message := capture.Combined()
	if err == nil {
		return message, nil
	}

	if verbosity < core.VerbosityDebug && stderr != nil {
		_, _ = io.WriteString(stderr, message)
	}
	if push && shared.IsDockerPushAuthorizationError(message) {
		return message, shared.DockerRegistryAuthError{
			Tag:      authContextTag,
			Registry: registrypkg.DockerRegistryFromImageTag(authContextTag),
			Message:  strings.TrimSpace(message),
			Err:      err,
		}
	}
	if diagnosis, ok := dockerBuildResourceExhaustionDiagnosis(message, err); ok {
		return message, DockerBuildResourceExhaustionError{Diagnosis: diagnosis, Err: err}
	}
	// The step's last words are all the durable timing record will have.
	reason := dockerBuildFailureReason(message)
	if diagnosis, ok := dockerBuildNetworkDiagnosis(message); ok {
		reason = joinFailureReason(reason, diagnosis)
	}
	if reason != "" {
		return message, DockerBuildStepError{Reason: reason, Err: err}
	}
	return message, err
}

func runDockerSimpleCommand(args []string, stdout, stderr io.Writer) error {
	cmd := core.Command("docker", args...)
	if stdout != nil {
		cmd.Stdout = stdout
	}
	if stderr != nil {
		cmd.Stderr = stderr
	}
	return cmd.Run()
}

// runDockerSimpleCommandWithVerbosity emulates --quiet, replaying captured output only on error.
func runDockerSimpleCommandWithVerbosity(args []string, verbosity int, stdout, stderr io.Writer) error {
	if verbosity >= core.VerbosityDebug {
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
	// A stale manifest list can hold the target tag ("AlreadyExists"); clearing either kind may fail harmlessly.
	_ = runDockerSimpleCommand([]string{"manifest", "rm", target}, io.Discard, io.Discard)
	_ = runDockerSimpleCommand([]string{"image", "rm", "-f", target}, io.Discard, io.Discard)
	return tryDockerTag(source, target, stdout, stderr)
}

func tryDockerTag(source, target string, stdout, stderr io.Writer) error {
	capture := new(bytes.Buffer)
	cmd := core.Command("docker", "tag", source, target)
	if stdout != nil {
		cmd.Stdout = stdout
	}
	cmd.Stderr = core.TeeWriter(stderr, capture)
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

func BuildScriptRunner(dir, scriptPath string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	// Windows cannot exec a shebang script by name, so run it through sh.
	cmd := core.Command(scriptPath)
	if runtime.GOOS == "windows" {
		cmd = core.Command("sh", scriptPath)
	}
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func DockerRegistryLogin(registry string, stdin io.Reader, stdout, stderr io.Writer) error {
	if registrypkg.IsGHCRRegistry(registry) {
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

	loginCmd := core.Command("docker", args...)
	loginCmd.Stdin = stdin
	loginCmd.Stdout = stdout
	loginCmd.Stderr = stderr
	return loginCmd.Run()
}

func isHostedRegistry(registry string) bool {
	return strings.EqualFold(strings.TrimSpace(registry), config.HostedRegistryHost)
}

// DockerRegistryLoginWithHostedRegistry logs in to the hosted registry with the operator's
// erun-api bearer token, deferring to DockerRegistryLogin otherwise.
func DockerRegistryLoginWithHostedRegistry(store config.CloudReadStore, deps cloud.CloudDependencies) DockerRegistryLoginFunc {
	return func(registry string, stdin io.Reader, stdout, stderr io.Writer) error {
		if isHostedRegistry(registry) {
			return hostedRegistryDockerLogin(store, deps, stdout, stderr)
		}
		return DockerRegistryLogin(registry, stdin, stdout, stderr)
	}
}

// hostedRegistryDockerLogin passes the token over stdin, never argv.
func hostedRegistryDockerLogin(store config.CloudReadStore, deps cloud.CloudDependencies, stdout, stderr io.Writer) error {
	provider, err := shared.ResolveERunPlatformAlias(store, "")
	if err != nil {
		return fmt.Errorf("erun's hosted registry authenticates with the tenant's own erun-api bearer token, minted from a configured erun cloud provider alias: %w", err)
	}
	token, err := cloud.CloudProviderBearerToken(core.Context{}, store, cloud.CloudBearerParams{Alias: provider.Alias}, deps)
	if err != nil {
		return fmt.Errorf("mint erun-api bearer token for hosted registry login: %w", err)
	}
	if strings.TrimSpace(token.Token) == "" {
		return fmt.Errorf("erun cloud provider alias %q returned an empty bearer token", provider.Alias)
	}
	loginCmd := core.Command("docker", "login", config.HostedRegistryHost, "-u", config.HostedRegistryLoginUsername, "--password-stdin")
	loginCmd.Stdin = strings.NewReader(token.Token)
	loginCmd.Stdout = stdout
	loginCmd.Stderr = stderr
	return loginCmd.Run()
}

func tryGHCRLoginViaGH(registry string, stdout, stderr io.Writer) (bool, error) {
	if !shared.GHCLIAvailable() {
		return false, nil
	}

	user, ok := shared.CaptureGHValue("api", "user", "--jq", ".login")
	if !ok {
		return false, nil
	}

	token, ok := shared.CaptureGHValue("auth", "token")
	if !ok {
		return false, nil
	}

	loginCmd := core.Command("docker", "login", "ghcr.io", "-u", user, "--password-stdin")
	loginCmd.Stdin = strings.NewReader(token)
	loginCmd.Stdout = stdout
	loginCmd.Stderr = stderr
	if err := loginCmd.Run(); err != nil {
		return false, err
	}
	return true, nil
}

// RefreshGHCRPackageScopes re-mints the gh token with write:packages; use it only after
// TryGHCRNamespaceLogin and a retry still fail scope-denied. It refuses non-interactive contexts.
func RefreshGHCRPackageScopes(tag string, stdin io.Reader, stdout, stderr io.Writer) (bool, error) {
	if !registrypkg.IsGHCRRegistry(registrypkg.DockerRegistryFromImageTag(tag)) {
		return false, nil
	}
	namespace := registrypkg.DockerNamespaceFromTag(tag)
	if namespace == "" {
		return false, nil
	}
	if !shared.GHCLIAvailable() {
		return false, nil
	}

	// gh's browser flow hangs forever without an operator, so bail before mutating gh state.
	if !interactiveGHAuthAllowed(stdin) {
		return false, newNonInteractiveGHCRScopeRefreshError(tag, namespace)
	}

	switchCmd := core.Command("gh", "auth", "switch", "-h", "github.com", "-u", namespace)
	switchCmd.Stdout = stdout
	switchCmd.Stderr = stderr
	switchErr := switchCmd.Run()

	var ghCmd *exec.Cmd
	if switchErr == nil {
		ghCmd = core.Command("gh", "auth", "refresh", "-h", "github.com", "-s", "write:packages,read:packages")
	} else {
		ghCmd = core.Command("gh", "auth", "login", "-h", "github.com", "-s", "write:packages,read:packages", "-w", "--git-protocol", "https")
	}
	ghCmd.Stdin = stdin
	ghCmd.Stdout = stdout
	ghCmd.Stderr = stderr
	if err := ghCmd.Run(); err != nil {
		return false, err
	}

	return shared.TryGHCRNamespaceLogin(tag, stdout, stderr)
}

// interactiveGHAuthAllowed is false in a runtime pod (PTY but no browser) and for a
// non-terminal stdin; ERUN_FORCE_TTY=1 is a test seam.
func interactiveGHAuthAllowed(stdin io.Reader) bool {
	if core.InInjectedRuntimePod() {
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

func newNonInteractiveGHCRScopeRefreshError(tag, namespace string) error {
	registry := registrypkg.DockerRegistryFromImageTag(tag)
	if registry == "" {
		registry = "ghcr.io"
	}
	reason := "stdin is not an interactive terminal"
	if core.InInjectedRuntimePod() {
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

func RunScriptSpec(ctx core.Context, script shared.ScriptSpec, run BuildScriptRunnerFunc) error {
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

func BuildScriptEnv(version string) []string {
	version = strings.TrimSpace(version)
	if version == "" {
		return nil
	}
	return []string{"ERUN_BUILD_VERSION=" + version}
}

func scriptTraceCommand(script shared.ScriptSpec) (string, []string) {
	if len(script.Env) == 0 {
		return script.Path, nil
	}

	args := append([]string{}, script.Env...)
	args = append(args, script.Path)
	return args[0], args[1:]
}
