package eruncommon

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeInflightMarkerFile writes a marker JSON file to path with the same
// encoding acquireHelmDeploySingleFlight itself uses, so tests can seed a
// pre-existing marker without going through the acquire path.
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

func testDeploySpec() HelmDeploySpec {
	return HelmDeploySpec{
		ReleaseName:       "acme-app",
		Namespace:         "acme-ns",
		KubernetesContext: "acme-ctx",
		ChartPath:         "oci://ghcr.io/sophium/charts/erun-devops",
	}
}

func markerPathFor(t *testing.T, configDir string, deploy HelmDeploySpec) string {
	t.Helper()
	deployDir := filepath.Join(configDir, deployInflightDirName)
	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		t.Fatalf("mkdir deploy dir: %v", err)
	}
	return filepath.Join(deployDir, helmDeployReleaseKey(deploy)+".json")
}

// TestDryRunReclaimsAnAgedMarkerWithALivePIDLikeTheRealRun proves the dry-run
// preview mirrors the real run's max-age reclaim arm: a marker older than
// helmDeploySingleFlightMaxAge with a live PID and identical params must report
// that it would proceed and reclaim, the same fate the real run reaches via
// reconcileExistingInflightMarker.
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
	ctx := Context{DryRun: true, Logger: NewLogger(0).WithTraceSink(&logBuf)}
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

// TestDryRunReclaimsAnAgedMarkerInsteadOfRefusingAsConcurrent proves the
// different-params case: without the max-age arm, dry-run refused with a
// concurrent-deploy error for a marker the real run would reclaim and deploy
// past. It must instead report Proceed with no error.
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
	ctx := Context{DryRun: true, Logger: NewLogger(0).WithTraceSink(&logBuf)}
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

// TestDryRunKeepsSkipBehaviorForAFreshMarkerWithIdenticalParams proves the fix
// does not touch today's behavior for a marker within the max age: identical
// params still report SkipDuplicate, and the marker is left untouched.
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
	ctx := Context{DryRun: true, Logger: NewLogger(0).WithTraceSink(&logBuf)}
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

// TestDryRunKeepsRefuseBehaviorForAFreshMarkerWithDifferentParams proves the
// fix does not touch today's behavior for a marker within the max age:
// different params still refuse with a concurrent-deploy error, and the
// marker is left untouched.
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

	ctx := Context{DryRun: true, Logger: NewLogger(0)}
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
