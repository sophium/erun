package dockerregistry

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
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

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
	args := append(dockerspec.DockerManifestArgs("inspect", insecure), tag)
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
	args := dockerspec.DockerPushArgs(tag, verbosity)
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
