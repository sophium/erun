package shared

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/helm"
	registrypkg "github.com/sophium/erun/erun-common/infra/registry"
)

const FingerprintTagPrefix = "fp-"

// DockerManifestArgs builds a `docker manifest <sub>` argv, appending
// --insecure when the registry is plain HTTP. Unlike the daemon (which reads
// its own insecure-registry list), `docker manifest create/push/inspect` talk
// to the registry directly over HTTPS by default and need the flag spelled
// out on every invocation that touches an insecure registry.
func DockerManifestArgs(sub string, insecure bool) []string {
	args := []string{"manifest", sub}
	if insecure {
		args = append(args, "--insecure")
	}
	return args
}

// StableBaseVersionTag returns the unsuffixed local tag a wrapper resolves from
// `FROM image:${ERUN_VERSION}`.
func StableBaseVersionTag(image helm.DockerImageReference) string {
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

func FingerprintTag(image helm.DockerImageReference, fingerprint, platform string) string {
	registry := strings.TrimSpace(image.Registry)
	repo := image.ImageName
	if registry != "" {
		repo = strings.TrimRight(registry, "/") + "/" + image.ImageName
	}
	suffix := FingerprintTagPrefix + fingerprint
	if platform != "" {
		suffix = suffix + "-" + platformShortSuffix(platform)
	}
	return repo + ":" + suffix
}

func PlatformSuffixedTag(tag, platform string) string {
	return tag + "-" + platformShortSuffix(platform)
}

func platformShortSuffix(platform string) string {
	parts := strings.SplitN(platform, "/", 2)
	if len(parts) == 2 && parts[1] != "" {
		return parts[1]
	}
	return strings.ReplaceAll(platform, "/", "-")
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
	cmd := core.Command("docker", "image", "inspect", tag)
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
	args := append(DockerManifestArgs("inspect", insecure), tag)
	cmd := core.Command("docker", args...)
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

func DockerBuildArgs(buildInput DockerBuildSpec, platform string) []string {
	tag := PlatformSuffixedTag(strings.TrimSpace(buildInput.Image.Tag), platform)
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
	args = append(args, dockerBuildEntitlementArgs(buildInput)...)
	args = append(args, "-t", tag)
	buildArgVersion := DockerBuildArgVersion(buildInput)
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
	if buildInput.DindCPULimit != "" {
		args = append(args, "--build-arg", "DIND_CPU_LIMIT="+buildInput.DindCPULimit)
	}
	if buildInput.DindMemoryLimitMiB != "" {
		args = append(args, "--build-arg", "DIND_MEMORY_LIMIT_MIB="+buildInput.DindMemoryLimitMiB)
	}
	if buildInput.CgroupParent != "" {
		args = append(args, "--cgroup-parent", buildInput.CgroupParent)
	}
	args = append(args, DockerSecretArgs(buildInput.DockerSecrets)...)
	args = append(args, "-f", buildInput.DockerfilePath, ".")
	return args
}

// dockerBuildEntitlementArgs returns the BuildKit entitlements this build is
// granted, empty when it is granted none.
//
// BuildKit default-denies a step that asks for `RUN --network=host`, failing
// the build at LLB load ("network.host is not allowed") before any step runs.
// Without the grant a Dockerfile test stage that starts a container fixture
// cannot build at all, which would leave the documented "a component's tests
// belong in that component's build/test stages" contract unsatisfiable rather
// than merely unfollowed.
//
// Scoped to a Dockerfile that declares a `test` stage, because that is the only
// place the grant is needed and it is a real capability rather than a harmless
// flag: it lets a build step join the *builder's* network namespace, which is
// this environment pod's — its loopback (where the unauthenticated dind
// listener sits), its address on the cluster network, and whatever else the pod
// routes to. Every other build erun issues is a production image with no tests
// in it, and a broad grant on those is a decision nobody made. A Dockerfile
// with no `test` stage therefore keeps the default deny, and fails loudly at
// LLB load if it asks anyway, rather than silently reaching the pod's network.
func dockerBuildEntitlementArgs(buildInput DockerBuildSpec) []string {
	if !DockerfileDeclaresTestStage(buildInput.DockerfilePath) {
		return nil
	}
	return []string{"--allow", "network.host"}
}

// DockerBuildArgVersion is the value the ERUN_VERSION build arg carries before
// any per-platform suffix: the stable base version when the build resolves a
// base locally, else the version in the image's own tag. Shared with the
// fingerprint so the identity a build is cached under and the version that build
// would bake in can never disagree.
func DockerBuildArgVersion(buildInput DockerBuildSpec) string {
	if base := strings.TrimSpace(buildInput.Image.BaseVersion); base != "" {
		return base
	}
	return DockerImageTagVersion(strings.TrimSpace(buildInput.Image.Tag))
}

func dockerPushArgs(tag string, verbosity int) []string {
	if verbosity >= core.VerbosityDebug {
		return []string{"push", tag}
	}
	return []string{"push", "--quiet", tag}
}

func DockerImageTagVersion(tag string) string {
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

// DockerPushUnknownBlobRetries bounds the re-push that absorbs a concurrent
// publisher. Two releases pushing overlapping layers to the same repository at
// the same time can have one lose the race: the registry reports a layer as
// present because the peer's upload of it is in flight but not yet committed,
// and the manifest that references that layer is then rejected with "unknown
// blob" even though this run built and uploaded everything it published. The
// condition is transient by construction — it clears once the peer's upload
// commits — so the same push is run again. Two retries cover a peer that is
// still mid-upload; past that the failure is not this race and belongs to the
// caller.
const DockerPushUnknownBlobRetries = 2

// dockerPushUnknownBlobBackoff is the pause before each retry, multiplied by
// the attempt number. It is wall-clock because the condition waited on (the
// peer's blob commit) lives in the registry; there is no local state to poll.
const dockerPushUnknownBlobBackoff = 2 * time.Second

// dockerPushWaitFunc pauses before a retry, injected so tests exercise the
// retry decision without paying the backoff.
type dockerPushWaitFunc func(attempt int)

func DockerImagePusher(tag string, verbosity int, stdout, stderr io.Writer) error {
	return PushDockerImageWithRetries(tag, verbosity, stdout, stderr, func(attempt int) {
		time.Sleep(time.Duration(attempt) * dockerPushUnknownBlobBackoff)
	})
}

// PushDockerImageWithRetries runs one push, then re-runs that same push for the two
// failures running it again can actually fix: a GHCR token without
// write:packages (after the namespace re-login that mints a better one), and
// the "unknown blob" cross-publisher race above. The two are distinct and do
// not compound — a blob rejection is not an authorization failure, so at most
// one of them applies to any given error.
//
// Every other failure is returned as it failed, on its first occurrence. The
// retry is gated on IsDockerUnknownBlobError, which matches only the
// registry-refused-a-blob-it-lacks shape; an auth, policy, or network failure
// never reaches the loop, so a genuine error cannot be hidden by it.
func PushDockerImageWithRetries(tag string, verbosity int, stdout, stderr io.Writer, wait dockerPushWaitFunc) error {
	err := RunDockerPushOnce(tag, verbosity, stdout, stderr)
	if err == nil {
		return nil
	}
	if shouldRetryAfterGHCRNamespaceLogin(err, tag, stdout, stderr) {
		if retryErr := RunDockerPushOnce(tag, verbosity, stdout, stderr); retryErr == nil {
			return nil
		} else {
			err = retryErr
		}
	}
	for attempt := 1; attempt <= DockerPushUnknownBlobRetries && IsDockerUnknownBlobError(err.Error()); attempt++ {
		_, _ = fmt.Fprintf(stderr, "==> push of %s was rejected with an unknown blob, which a concurrent publisher pushing the same repository can cause while its layer upload is still committing; re-pushing (%d/%d)\n", tag, attempt, DockerPushUnknownBlobRetries)
		wait(attempt)
		if retryErr := RunDockerPushOnce(tag, verbosity, stdout, stderr); retryErr == nil {
			return nil
		} else {
			err = retryErr
		}
	}
	return err
}

func RunDockerPushOnce(tag string, verbosity int, stdout, stderr io.Writer) error {
	args := dockerPushArgs(tag, verbosity)
	pushCmd := core.Command("docker", args...)
	capture := &core.CommandOutputCapture{}
	pushCmd.Stdout = core.TeeWriter(stdout, &capture.Stdout)
	pushCmd.Stderr = core.TeeWriter(stderr, &capture.Stderr)
	err := pushCmd.Run()
	if err == nil {
		return nil
	}

	message := capture.Combined()
	if IsDockerPushAuthorizationError(message) {
		return DockerRegistryAuthError{
			Tag:      tag,
			Registry: registrypkg.DockerRegistryFromImageTag(tag),
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

// TryGHCRNamespaceLogin re-authenticates docker to ghcr.io as the gh-configured
// user that owns the target namespace. Call it after a push fails with
// IsDockerCreatePackageDenied, to try an automatic auth switch before falling
// back to an interactive prompt.
func TryGHCRNamespaceLogin(tag string, stdout, stderr io.Writer) (bool, error) {
	if !registrypkg.IsGHCRRegistry(registrypkg.DockerRegistryFromImageTag(tag)) {
		return false, nil
	}
	namespace := registrypkg.DockerNamespaceFromTag(tag)
	if namespace == "" {
		return false, nil
	}
	if !GHCLIAvailable() {
		return false, nil
	}

	token, ok := CaptureGHValue("auth", "token", "-u", namespace, "-h", "github.com")
	if !ok {
		return false, nil
	}

	loginCmd := core.Command("docker", "login", "ghcr.io", "-u", namespace, "--password-stdin")
	loginCmd.Stdin = strings.NewReader(token)
	loginCmd.Stdout = stdout
	loginCmd.Stderr = stderr
	if err := loginCmd.Run(); err != nil {
		return false, err
	}
	return true, nil
}

// GHCLIAvailable reports whether the gh CLI is on PATH.
func GHCLIAvailable() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// CaptureGHValue reports gh's trimmed output, or false when gh fails or prints
// nothing: an unusable gh login is not an error, just no credential.
func CaptureGHValue(args ...string) (string, bool) {
	value, err := registrypkg.CaptureGHCommand(args...)
	if err != nil {
		return "", false
	}
	value = strings.TrimSpace(value)
	return value, value != ""
}
