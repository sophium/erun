package eruncommon

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// registryVerifyMaxAttempts bounds a post-push read-back and
// registryVerifyRetryBase is its linear backoff step, so the worst case costs a
// few seconds rather than stalling a publish.
// A tag erun just pushed is not always immediately readable: the registry can
// mint the pull token before the new tag has propagated and answer the first
// fetch 403 denied. Verification reads back an object erun itself wrote, so a
// transient-looking read is a propagation race, not a verdict — a few fast
// attempts clear it, while a genuinely unreadable artifact still fails.
const (
	registryVerifyMaxAttempts = 4
	registryVerifyRetryBase   = 500 * time.Millisecond
)

// VerifyPublishedHelmChart re-pulls the just-pushed chart so a release never
// assumes remote state: the artifact later steps and consuming envs depend on
// must be provably fetchable.
func VerifyPublishedHelmChart(ctx Context, ociRepo, chartName, version string) error {
	destination := filepath.Join(os.TempDir(), "erun-chart-verify")
	args := []string{
		"pull",
		strings.TrimSuffix(strings.TrimSpace(ociRepo), "/") + "/" + chartName,
		"--version", version,
		"--destination", destination,
	}
	ctx.TraceCommand("", "helm", args...)
	if ctx.DryRun {
		return nil
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	defer func() { _ = os.Remove(filepath.Join(destination, chartName+"-"+version+".tgz")) }()

	spec := commandSpec{Name: "helm", Args: args}
	if err := readBackPublishedArtifact(ctx, "chart "+chartName+" "+version, func() (string, error) {
		return runHelmCommandCapturingOutput(ctx, spec)
	}); err != nil {
		return fmt.Errorf("verify published chart %s:%s: %w", chartName, version, err)
	}
	ctx.Info("==> Verified published chart " + chartName + " " + version)
	return nil
}

// VerifyPublishedDockerImage re-resolves the multi-arch manifest just pushed for
// a tag. Nothing else proves the image half of a version exists, and a release
// that assumed it does is how a tag gets announced for an image no deploy can
// pull.
func VerifyPublishedDockerImage(ctx Context, tag string, insecure bool) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return nil
	}
	spec := commandSpec{Name: "docker", Args: append(dockerManifestArgs("inspect", insecure), tag)}
	ctx.TraceCommand(spec.Dir, spec.Name, spec.Args...)
	if ctx.DryRun {
		return nil
	}
	if err := readBackPublishedArtifact(ctx, "image "+tag, func() (string, error) {
		return runCommandCapturingOutput(ctx, spec)
	}); err != nil {
		return fmt.Errorf("verify published image %s: %w", tag, err)
	}
	ctx.Info("==> Verified published image " + tag)
	return nil
}

func readBackPublishedArtifact(ctx Context, describe string, read func() (string, error)) error {
	var lastErr error
	for attempt := 1; attempt <= registryVerifyMaxAttempts; attempt++ {
		output, err := read()
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt == registryVerifyMaxAttempts || !isTransientRegistryReadError(output) {
			break
		}
		delay := registryVerifyRetryBase * time.Duration(attempt)
		ctx.Info(fmt.Sprintf("==> Published %s not readable yet; retrying in %s (attempt %d of %d)", describe, delay, attempt+1, registryVerifyMaxAttempts))
		time.Sleep(delay)
	}
	return lastErr
}

// isTransientRegistryReadError classifies a failed read-back of an artifact erun
// just pushed. Authorization and not-found answers are the shape registry
// read-after-write propagation takes (the pull token is minted for a tag the
// backend has not yet listed), and transport failures are transient by nature;
// anything else is treated as final.
func isTransientRegistryReadError(output string) bool {
	message := strings.ToLower(output)
	for _, marker := range []string{
		"401", "403", "404",
		"denied", "unauthorized", "not found", "manifest unknown",
		"timeout", "timed out", "temporary failure", "connection reset",
		"connection refused", "eof", "no such host", "tls handshake",
		"service unavailable", "too many requests", "500 ", "502", "503",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// runCommandCapturingOutput keeps a verification read quiet on success — the
// manifest body is not what the operator asked for — while still surfacing the
// tool's own diagnostics and capturing everything for failure classification.
func runCommandCapturingOutput(ctx Context, spec commandSpec) (string, error) {
	capture := new(bytes.Buffer)
	cmd := Command(spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Stdout = capture
	cmd.Stderr = commandOutputWriter(ctx.Stderr, capture)
	err := cmd.Run()
	return capture.String(), err
}
