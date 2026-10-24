package deploy

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam"
)

func writeInflightMarkerFile(t *testing.T, path string, record deployInflightRecord) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create marker file: %v", err)
	}
	defer func() { _ = f.Close() }()
	if err := writeInflightRecord(f, record); err != nil {
		t.Fatalf("write marker record: %v", err)
	}
}

func testDeploySpec() seam.HelmDeploySpec {
	return seam.HelmDeploySpec{
		ReleaseName:       "acme-app",
		Namespace:         "acme-ns",
		KubernetesContext: "acme-ctx",
		ChartPath:         "oci://ghcr.io/sophium/charts/erun-devops",
	}
}

func markerPathFor(t *testing.T, configDir string, deploy seam.HelmDeploySpec) string {
	t.Helper()
	deployDir := filepath.Join(configDir, deployInflightDirName)
	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		t.Fatalf("mkdir deploy dir: %v", err)
	}
	return filepath.Join(deployDir, helmDeployReleaseKey(deploy)+".json")
}

func TestDryRunReclaimsAnAgedMarkerWithALivePIDLikeTheRealRun(t *testing.T) {
	configDir := t.TempDir()
	deploy := testDeploySpec()
	path := markerPathFor(t, configDir, deploy)
	paramsHash := helmDeployParamsHash(deploy)

	startedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	record := deployInflightRecord{
		PID:        4242,
		StartedAt:  startedAt,
		ParamsHash: paramsHash,
	}
	writeInflightMarkerFile(t, path, record)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read marker before: %v", err)
	}

	var logBuf bytes.Buffer
	ctx := core.Context{DryRun: true, Logger: core.NewLogger(0).WithTraceSink(&logBuf)}
	deps := helmDeploySingleFlightDeps{
		configDir: func() (string, error) { return configDir, nil },
		now:       func() time.Time { return startedAt.Add(helmDeploySingleFlightMaxAge + time.Minute) },
		isAlive:   func(int) bool { return true },
	}

	outcome, handle, err := acquireHelmDeploySingleFlight(ctx, deploy, deps)
	if err != nil {
		t.Fatalf("acquire: unexpected error: %v", err)
	}
	if handle != nil {
		t.Fatalf("dry-run must never return a real handle")
	}
	if outcome != HelmDeploySingleFlightProceed {
		t.Fatalf("outcome = %v, want Proceed (would reclaim)", outcome)
	}
	if !bytes.Contains(logBuf.Bytes(), []byte("would reclaim")) {
		t.Fatalf("trace = %q, want it to mention 'would reclaim'", logBuf.String())
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read marker after: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("dry-run mutated the on-disk marker: before=%q after=%q", before, after)
	}
}

func TestDryRunReclaimsAnAgedMarkerInsteadOfRefusingAsConcurrent(t *testing.T) {
	configDir := t.TempDir()
	deploy := testDeploySpec()
	path := markerPathFor(t, configDir, deploy)

	startedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	record := deployInflightRecord{
		PID:        4242,
		StartedAt:  startedAt,
		ParamsHash: "some-other-hash",
	}
	writeInflightMarkerFile(t, path, record)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read marker before: %v", err)
	}

	var logBuf bytes.Buffer
	ctx := core.Context{DryRun: true, Logger: core.NewLogger(0).WithTraceSink(&logBuf)}
	deps := helmDeploySingleFlightDeps{
		configDir: func() (string, error) { return configDir, nil },
		now:       func() time.Time { return startedAt.Add(helmDeploySingleFlightMaxAge + time.Minute) },
		isAlive:   func(int) bool { return true },
	}

	outcome, handle, err := acquireHelmDeploySingleFlight(ctx, deploy, deps)
	if err != nil {
		t.Fatalf("acquire: want Proceed with no error (would reclaim), got error: %v", err)
	}
	if handle != nil {
		t.Fatalf("dry-run must never return a real handle")
	}
	if outcome != HelmDeploySingleFlightProceed {
		t.Fatalf("outcome = %v, want Proceed (would reclaim), not a concurrent-deploy refusal", outcome)
	}
	if !bytes.Contains(logBuf.Bytes(), []byte("would reclaim")) {
		t.Fatalf("trace = %q, want it to mention 'would reclaim'", logBuf.String())
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read marker after: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("dry-run mutated the on-disk marker: before=%q after=%q", before, after)
	}
}

func TestDryRunKeepsSkipBehaviorForAFreshMarkerWithIdenticalParams(t *testing.T) {
	startedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fixedNow := startedAt.Add(time.Minute)

	configDir := t.TempDir()
	deploy := testDeploySpec()
	path := markerPathFor(t, configDir, deploy)
	paramsHash := helmDeployParamsHash(deploy)

	writeInflightMarkerFile(t, path, deployInflightRecord{
		PID:        4242,
		StartedAt:  startedAt,
		ParamsHash: paramsHash,
	})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read marker before: %v", err)
	}

	var logBuf bytes.Buffer
	ctx := core.Context{DryRun: true, Logger: core.NewLogger(0).WithTraceSink(&logBuf)}
	deps := helmDeploySingleFlightDeps{
		configDir: func() (string, error) { return configDir, nil },
		now:       func() time.Time { return fixedNow },
		isAlive:   func(int) bool { return true },
	}

	outcome, handle, err := acquireHelmDeploySingleFlight(ctx, deploy, deps)
	if err != nil {
		t.Fatalf("acquire: unexpected error: %v", err)
	}
	if handle != nil {
		t.Fatalf("dry-run must never return a real handle")
	}
	if outcome != HelmDeploySingleFlightSkipDuplicate {
		t.Fatalf("outcome = %v, want SkipDuplicate", outcome)
	}
	if !bytes.Contains(logBuf.Bytes(), []byte("would skip")) {
		t.Fatalf("trace = %q, want it to mention 'would skip'", logBuf.String())
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read marker after: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("dry-run mutated the on-disk marker: before=%q after=%q", before, after)
	}
}

func TestDryRunKeepsRefuseBehaviorForAFreshMarkerWithDifferentParams(t *testing.T) {
	startedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fixedNow := startedAt.Add(time.Minute)

	configDir := t.TempDir()
	deploy := testDeploySpec()
	path := markerPathFor(t, configDir, deploy)

	writeInflightMarkerFile(t, path, deployInflightRecord{
		PID:        4242,
		StartedAt:  startedAt,
		ParamsHash: "some-other-hash",
	})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read marker before: %v", err)
	}

	ctx := core.Context{DryRun: true, Logger: core.NewLogger(0)}
	deps := helmDeploySingleFlightDeps{
		configDir: func() (string, error) { return configDir, nil },
		now:       func() time.Time { return fixedNow },
		isAlive:   func(int) bool { return true },
	}

	outcome, handle, err := acquireHelmDeploySingleFlight(ctx, deploy, deps)
	if err == nil {
		t.Fatalf("acquire: want a concurrent-deploy error, got none (outcome=%v)", outcome)
	}
	var concurrentErr *HelmReleaseConcurrentDeployError
	if !errors.As(err, &concurrentErr) {
		t.Fatalf("acquire: want *HelmReleaseConcurrentDeployError, got %T: %v", err, err)
	}
	if handle != nil {
		t.Fatalf("dry-run must never return a real handle")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read marker after: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("dry-run mutated the on-disk marker: before=%q after=%q", before, after)
	}
}
