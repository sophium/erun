package eruncommon

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestSingleFlightContext() (Context, *bytes.Buffer) {
	traceBuf := &bytes.Buffer{}
	ctx := Context{
		Logger: NewLoggerWithWriters(2, traceBuf, traceBuf),
		Stdout: io.Discard,
		Stderr: io.Discard,
	}
	return ctx, traceBuf
}

func TestAcquireHelmDeploySingleFlightProceeds(t *testing.T) {
	dir := t.TempDir()
	deploy := HelmDeploySpec{
		ReleaseName:       "erun-devops",
		Namespace:         "erun-ux",
		KubernetesContext: "erun",
		Tenant:            "erun",
		Environment:       "ux",
		Version:           "1.0.51",
		ChartPath:         "/charts/erun-devops",
		Timeout:           "2m0s",
	}
	ctx, traceBuf := newTestSingleFlightContext()

	deps := helmDeploySingleFlightDeps{
		configDir: func() (string, error) { return dir, nil },
		now:       func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
		pid:       func() int { return 1234 },
		isAlive:   func(int) bool { return true },
	}
	outcome, handle, err := acquireHelmDeploySingleFlight(ctx, deploy, deps)
	if err != nil {
		t.Fatalf("acquire returned unexpected error: %v", err)
	}
	if outcome != HelmDeploySingleFlightProceed {
		t.Fatalf("outcome = %d, want %d", outcome, HelmDeploySingleFlightProceed)
	}
	if handle == nil || handle.path == "" {
		t.Fatal("expected non-empty handle")
	}
	if _, statErr := os.Stat(handle.path); statErr != nil {
		t.Fatalf("marker not created: %v", statErr)
	}
	if !strings.Contains(traceBuf.String(), "dedup: claim") {
		t.Fatalf("expected dedup-claim trace, got: %q", traceBuf.String())
	}
	handle.Release()
	if _, statErr := os.Stat(handle.path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("marker not removed after Release: %v", statErr)
	}
}

func TestAcquireHelmDeploySingleFlightSkipsIdentical(t *testing.T) {
	dir := t.TempDir()
	deploy := HelmDeploySpec{
		ReleaseName:       "erun-devops",
		Namespace:         "erun-ux",
		KubernetesContext: "erun",
		Tenant:            "erun",
		Environment:       "ux",
		Version:           "1.0.51",
		ChartPath:         "/charts/erun-devops",
		Timeout:           "2m0s",
	}
	firstCtx, _ := newTestSingleFlightContext()
	first, firstHandle, err := acquireHelmDeploySingleFlight(firstCtx, deploy, helmDeploySingleFlightDeps{
		configDir: func() (string, error) { return dir, nil },
		now:       func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
		pid:       func() int { return 4321 },
		isAlive:   func(int) bool { return true },
	})
	if err != nil || first != HelmDeploySingleFlightProceed {
		t.Fatalf("first acquire failed: outcome=%d err=%v", first, err)
	}
	t.Cleanup(firstHandle.Release)

	secondCtx, traceBuf := newTestSingleFlightContext()
	outcome, handle, err := acquireHelmDeploySingleFlight(secondCtx, deploy, helmDeploySingleFlightDeps{
		configDir: func() (string, error) { return dir, nil },
		now:       func() time.Time { return time.Date(2026, 5, 10, 12, 0, 5, 0, time.UTC) },
		pid:       func() int { return 9999 },
		isAlive:   func(pid int) bool { return pid == 4321 },
	})
	if err != nil {
		t.Fatalf("second acquire returned unexpected error: %v", err)
	}
	if outcome != HelmDeploySingleFlightSkipDuplicate {
		t.Fatalf("outcome = %d, want %d", outcome, HelmDeploySingleFlightSkipDuplicate)
	}
	if handle != nil {
		t.Fatalf("expected nil handle on skip, got %+v", handle)
	}
	if !strings.Contains(traceBuf.String(), "dedup: skip") {
		t.Fatalf("expected dedup-skip trace, got: %q", traceBuf.String())
	}
}

func TestAcquireHelmDeploySingleFlightConflictsOnDifferentParams(t *testing.T) {
	dir := t.TempDir()
	deploy := HelmDeploySpec{
		ReleaseName:       "erun-devops",
		Namespace:         "erun-ux",
		KubernetesContext: "erun",
		Tenant:            "erun",
		Environment:       "ux",
		Version:           "1.0.51",
		ChartPath:         "/charts/erun-devops",
		Timeout:           "2m0s",
	}
	firstCtx, _ := newTestSingleFlightContext()
	_, firstHandle, err := acquireHelmDeploySingleFlight(firstCtx, deploy, helmDeploySingleFlightDeps{
		configDir: func() (string, error) { return dir, nil },
		now:       func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
		pid:       func() int { return 4321 },
		isAlive:   func(int) bool { return true },
	})
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	t.Cleanup(firstHandle.Release)

	conflicting := deploy
	conflicting.Version = "1.0.52"
	conflicting.ImageOverrides = map[string]string{"erun-dind": "ghcr.io/sophium/erun-dind:other"}

	secondCtx, _ := newTestSingleFlightContext()
	_, _, err = acquireHelmDeploySingleFlight(secondCtx, conflicting, helmDeploySingleFlightDeps{
		configDir: func() (string, error) { return dir, nil },
		now:       func() time.Time { return time.Date(2026, 5, 10, 12, 1, 0, 0, time.UTC) },
		pid:       func() int { return 9999 },
		isAlive:   func(pid int) bool { return pid == 4321 },
	})
	var conflict *HelmReleaseConcurrentDeployError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected HelmReleaseConcurrentDeployError, got %v", err)
	}
	if conflict.OtherPID != 4321 {
		t.Fatalf("conflict.OtherPID = %d, want 4321", conflict.OtherPID)
	}
	if !strings.Contains(conflict.Error(), "another erun deploy") {
		t.Fatalf("error message missing prefix: %q", conflict.Error())
	}
}

func TestAcquireHelmDeploySingleFlightReclaimsDeadPID(t *testing.T) {
	dir := t.TempDir()
	deploy := HelmDeploySpec{
		ReleaseName:       "erun-devops",
		Namespace:         "erun-ux",
		KubernetesContext: "erun",
		Tenant:            "erun",
		Environment:       "ux",
		Version:           "1.0.51",
		ChartPath:         "/charts/erun-devops",
		Timeout:           "2m0s",
	}
	firstCtx, _ := newTestSingleFlightContext()
	_, _, err := acquireHelmDeploySingleFlight(firstCtx, deploy, helmDeploySingleFlightDeps{
		configDir: func() (string, error) { return dir, nil },
		now:       func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
		pid:       func() int { return 4321 },
		isAlive:   func(int) bool { return true },
	})
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	secondCtx, traceBuf := newTestSingleFlightContext()
	outcome, handle, err := acquireHelmDeploySingleFlight(secondCtx, deploy, helmDeploySingleFlightDeps{
		configDir: func() (string, error) { return dir, nil },
		now:       func() time.Time { return time.Date(2026, 5, 10, 12, 5, 0, 0, time.UTC) },
		pid:       func() int { return 9999 },
		isAlive:   func(pid int) bool { return false },
	})
	if err != nil {
		t.Fatalf("reclaim acquire failed: %v", err)
	}
	if outcome != HelmDeploySingleFlightProceed {
		t.Fatalf("outcome = %d, want Proceed", outcome)
	}
	if handle == nil {
		t.Fatal("expected handle on reclaim")
	}
	t.Cleanup(handle.Release)
	if !strings.Contains(traceBuf.String(), "dedup: reclaim") {
		t.Fatalf("expected reclaim trace, got: %q", traceBuf.String())
	}
}

func TestAcquireHelmDeploySingleFlightDryRunReportsConflict(t *testing.T) {
	dir := t.TempDir()
	deploy := HelmDeploySpec{
		ReleaseName:       "erun-devops",
		Namespace:         "erun-ux",
		KubernetesContext: "erun",
		Tenant:            "erun",
		Environment:       "ux",
		Version:           "1.0.51",
		ChartPath:         "/charts/erun-devops",
		Timeout:           "2m0s",
	}
	otherCtx, _ := newTestSingleFlightContext()
	_, otherHandle, err := acquireHelmDeploySingleFlight(otherCtx, deploy, helmDeploySingleFlightDeps{
		configDir: func() (string, error) { return dir, nil },
		now:       func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
		pid:       func() int { return 4321 },
		isAlive:   func(int) bool { return true },
	})
	if err != nil {
		t.Fatalf("seed acquire failed: %v", err)
	}
	t.Cleanup(otherHandle.Release)

	conflicting := deploy
	conflicting.Version = "1.0.52"
	conflicting.ImageOverrides = map[string]string{"erun-dind": "ghcr.io/sophium/erun-dind:other"}
	dryCtx, _ := newTestSingleFlightContext()
	dryCtx.DryRun = true
	_, _, err = acquireHelmDeploySingleFlight(dryCtx, conflicting, helmDeploySingleFlightDeps{
		configDir: func() (string, error) { return dir, nil },
		now:       func() time.Time { return time.Date(2026, 5, 10, 12, 1, 0, 0, time.UTC) },
		pid:       func() int { return 9999 },
		isAlive:   func(pid int) bool { return pid == 4321 },
	})
	var conflict *HelmReleaseConcurrentDeployError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected HelmReleaseConcurrentDeployError in dry-run, got %v", err)
	}
}

func TestAcquireHelmDeploySingleFlightDryRunReportsSkip(t *testing.T) {
	dir := t.TempDir()
	deploy := HelmDeploySpec{
		ReleaseName:       "erun-devops",
		Namespace:         "erun-ux",
		KubernetesContext: "erun",
		Tenant:            "erun",
		Environment:       "ux",
		Version:           "1.0.51",
		ChartPath:         "/charts/erun-devops",
		Timeout:           "2m0s",
	}
	otherCtx, _ := newTestSingleFlightContext()
	_, otherHandle, err := acquireHelmDeploySingleFlight(otherCtx, deploy, helmDeploySingleFlightDeps{
		configDir: func() (string, error) { return dir, nil },
		now:       func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
		pid:       func() int { return 4321 },
		isAlive:   func(int) bool { return true },
	})
	if err != nil {
		t.Fatalf("seed acquire failed: %v", err)
	}
	t.Cleanup(otherHandle.Release)

	dryCtx, traceBuf := newTestSingleFlightContext()
	dryCtx.DryRun = true
	outcome, handle, err := acquireHelmDeploySingleFlight(dryCtx, deploy, helmDeploySingleFlightDeps{
		configDir: func() (string, error) { return dir, nil },
		now:       func() time.Time { return time.Date(2026, 5, 10, 12, 0, 5, 0, time.UTC) },
		pid:       func() int { return 9999 },
		isAlive:   func(pid int) bool { return pid == 4321 },
	})
	if err != nil {
		t.Fatalf("dry-run acquire returned unexpected error: %v", err)
	}
	if outcome != HelmDeploySingleFlightSkipDuplicate {
		t.Fatalf("outcome = %d, want SkipDuplicate", outcome)
	}
	if handle != nil {
		t.Fatalf("expected nil handle in dry-run, got %+v", handle)
	}
	if !strings.Contains(traceBuf.String(), "dedup: would skip") {
		t.Fatalf("expected dry-run skip trace, got: %q", traceBuf.String())
	}
}

func TestAcquireHelmDeploySingleFlightDryRunDoesNotPersist(t *testing.T) {
	dir := t.TempDir()
	deploy := HelmDeploySpec{
		ReleaseName:       "erun-devops",
		Namespace:         "erun-ux",
		KubernetesContext: "erun",
		Tenant:            "erun",
		Environment:       "ux",
		Version:           "1.0.51",
		ChartPath:         "/charts/erun-devops",
		Timeout:           "2m0s",
	}
	ctx, traceBuf := newTestSingleFlightContext()
	ctx.DryRun = true

	outcome, handle, err := acquireHelmDeploySingleFlight(ctx, deploy, helmDeploySingleFlightDeps{
		configDir: func() (string, error) { return dir, nil },
		now:       func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
		pid:       func() int { return 4321 },
		isAlive:   func(int) bool { return true },
	})
	if err != nil {
		t.Fatalf("acquire returned unexpected error: %v", err)
	}
	if outcome != HelmDeploySingleFlightProceed {
		t.Fatalf("outcome = %d, want Proceed", outcome)
	}
	if handle != nil {
		t.Fatalf("expected nil handle in dry-run, got %+v", handle)
	}
	if !strings.Contains(traceBuf.String(), "dedup: ready") {
		t.Fatalf("expected dry-run ready trace, got: %q", traceBuf.String())
	}
	deployDir := filepath.Join(dir, deployInflightDirName)
	if _, statErr := os.Stat(deployDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("dry-run created on-disk state: %v", statErr)
	}
}

func TestHelmDeployParamsHashStable(t *testing.T) {
	deploy := HelmDeploySpec{
		ReleaseName:       "erun-devops",
		Namespace:         "erun-ux",
		KubernetesContext: "erun",
		Tenant:            "erun",
		Environment:       "ux",
		Version:           "1.0.51",
		ChartPath:         "/charts/erun-devops",
		Timeout:           "2m0s",
		ImageOverrides: map[string]string{
			"erun-dind":   "ghcr.io/sophium/erun-dind:28.1.1-1",
			"erun-devops": "ghcr.io/sophium/erun-devops:1.0.51",
		},
	}
	first := helmDeployParamsHash(deploy)
	deploy.ImageOverrides = map[string]string{
		"erun-devops": "ghcr.io/sophium/erun-devops:1.0.51",
		"erun-dind":   "ghcr.io/sophium/erun-dind:28.1.1-1",
	}
	second := helmDeployParamsHash(deploy)
	if first != second {
		t.Fatalf("hash drift across map orderings: %s vs %s", first, second)
	}
	deploy.ImageOverrides["erun-dind"] = "ghcr.io/sophium/erun-dind:other"
	if helmDeployParamsHash(deploy) == first {
		t.Fatalf("hash did not change when image override changed")
	}
}

func TestHelmDeployReleaseKeySanitizes(t *testing.T) {
	got := helmDeployReleaseKey(HelmDeploySpec{
		KubernetesContext: "arn:aws:eks:eu-west-1:000000000000:cluster/erun",
		Namespace:         "erun-ux",
		ReleaseName:       "erun-devops",
	})
	if strings.ContainsAny(got, "/:") {
		t.Fatalf("release key contains unsafe filename characters: %q", got)
	}
}
