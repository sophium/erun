package shared

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	registrypkg "github.com/sophium/erun/erun-common/infra/registry"
)

const FingerprintTagPrefix = "fp-"

// DockerManifestArgs needs --insecure spelled out because `docker manifest`
// talks to the registry directly, ignoring the daemon's insecure list.
func DockerManifestArgs(sub string, insecure bool) []string {
	args := []string{"manifest", sub}
	if insecure {
		args = append(args, "--insecure")
	}
	return args
}

// StableBaseVersionTag returns the unsuffixed local tag a wrapper resolves from
// `FROM image:${ERUN_VERSION}`.
func StableBaseVersionTag(image model.DockerImageReference) string {
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

func FingerprintTag(image model.DockerImageReference, fingerprint, platform string) string {
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
	var authErr model.DockerRegistryAuthError
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
	// Provenance would make each per-arch tag a manifest list, which
	// `docker manifest create` rejects.
	args := []string{"build", "--platform", platform, "--provenance=false"}
	// Never --quiet: it suppresses a failing step's output at the source.
	args = append(args, "--progress=plain")
	args = append(args, dockerBuildEntitlementArgs(buildInput)...)
	args = append(args, "-t", tag)
	buildArgVersion := DockerBuildArgVersion(buildInput)
	// A local base resolves only under its per-arch tags; the arch-less tag
	// names just the last arch built.
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

// dockerBuildEntitlementArgs grants network.host only to Dockerfiles with a
// `test` stage, which need it for container fixtures; it exposes the pod's
// network, including the unauthenticated dind listener.
func dockerBuildEntitlementArgs(buildInput DockerBuildSpec) []string {
	if !DockerfileDeclaresTestStage(buildInput.DockerfilePath) {
		return nil
	}
	return []string{"--allow", "network.host"}
}

// DockerBuildArgVersion is shared with the fingerprint so the cache identity
// and the baked-in version cannot disagree.
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

// DockerPushUnknownBlobRetries absorbs a concurrent publisher whose in-flight
// layer upload makes our manifest fail with "unknown blob".
const DockerPushUnknownBlobRetries = 2

// dockerPushUnknownBlobBackoff is multiplied by the attempt number.
const dockerPushUnknownBlobBackoff = 2 * time.Second

type dockerPushWaitFunc func(attempt int)

func DockerImagePusher(tag string, verbosity int, stdout, stderr io.Writer) error {
	return PushDockerImageWithRetries(tag, verbosity, stdout, stderr, func(attempt int) {
		time.Sleep(time.Duration(attempt) * dockerPushUnknownBlobBackoff)
	})
}

// PushDockerImageWithRetries retries only a GHCR scope failure (after
// re-login) and the unknown-blob race; anything else returns at once.
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
		return model.DockerRegistryAuthError{
			Tag:      tag,
			Registry: registrypkg.DockerRegistryFromImageTag(tag),
			Message:  strings.TrimSpace(message),
			Err:      err,
		}
	}
	return dockerPushError{tag: tag, err: err, message: message}
}

// dockerPushError names the tag a bare registry response belongs to.
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

// TryGHCRNamespaceLogin re-authenticates as the gh user owning the namespace.
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

func GHCLIAvailable() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// CaptureGHValue treats an unusable gh login as no credential, not an error.
func CaptureGHValue(args ...string) (string, bool) {
	value, err := registrypkg.CaptureGHCommand(args...)
	if err != nil {
		return "", false
	}
	value = strings.TrimSpace(value)
	return value, value != ""
}
