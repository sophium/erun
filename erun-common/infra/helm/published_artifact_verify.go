package helm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

// A just-pushed tag can briefly answer 403 before it propagates, so read-back
// retries a few times with linear backoff.
const (
	RegistryVerifyMaxAttempts = 4
	registryVerifyRetryBase   = 500 * time.Millisecond
)

// VerifyPublishedHelmChart re-pulls the pushed chart to prove it is fetchable.
func VerifyPublishedHelmChart(ctx core.Context, ociRepo, chartName, version string) error {
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

	spec := CommandSpec{Name: "helm", Args: args}
	if err := ReadBackPublishedArtifact(ctx, "chart "+chartName+" "+version, func() (string, error) {
		return RunHelmCommandCapturingOutput(ctx, spec)
	}); err != nil {
		return fmt.Errorf("verify published chart %s:%s: %w", chartName, version, err)
	}
	ctx.Info("==> Verified published chart " + chartName + " " + version)
	return nil
}

func ReadBackPublishedArtifact(ctx core.Context, describe string, read func() (string, error)) error {
	var lastErr error
	for attempt := 1; attempt <= RegistryVerifyMaxAttempts; attempt++ {
		output, err := read()
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt == RegistryVerifyMaxAttempts || !IsTransientRegistryReadError(output) {
			break
		}
		delay := registryVerifyRetryBase * time.Duration(attempt)
		ctx.Info(fmt.Sprintf("==> Published %s not readable yet; retrying in %s (attempt %d of %d)", describe, delay, attempt+1, RegistryVerifyMaxAttempts))
		time.Sleep(delay)
	}
	return lastErr
}

// IsTransientRegistryReadError treats auth, not-found, and transport failures as
// propagation races; anything else is final.
func IsTransientRegistryReadError(output string) bool {
	message := strings.ToLower(output)
	for _, marker := range []string{
		"401", "403", "404",
		"denied", "unauthorized", "not found", "manifest unknown",
		"timeout", "timed out", "temporary failure", "connection reset",
		"connection refused", "eof", "no such host", "tls handshake",
		"service unavailable", "too many requests", "500", "502", "503",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
