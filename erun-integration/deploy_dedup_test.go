package integration

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/harnessexec"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testDeployRealRunDedupSkipWhenIdenticalMarkerAlive(t *testing.T) {
	// -vv is required: the params hash embeds verbosity, and dry-run always traces
	// raised verbosity. The marker must be fresh or the max-age reclaim fires first.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	first := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if first.ExitCode != 0 {
		t.Fatalf("first dry-run exited %d:\n%s", first.ExitCode, first.Combined)
	}
	hash := extractDedupHash(t, first.Combined)
	fixture.SeedDeployInflightMarker(t, setup, "test-context", "team-dev", "team-devops", fixture.DeployInflightRecord{
		PID:         os.Getpid(),
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
		ParamsHash:  hash,
		Tenant:      "team",
		Environment: "dev",
		Version:     "1.0.0",
	})
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/real_run_dedup_skip_when_identical_marker_alive", normalize.Apply(result.Combined))
}

func testDeployRealRunDedupReclaimsAgedMarker(t *testing.T) {
	// The reclaim trace embeds the marker's wall-clock age, so only stable fragments are asserted.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDeployInflightMarker(t, setup, "test-context", "team-dev", "team-devops", fixture.DeployInflightRecord{
		PID:         os.Getpid(),
		ParamsHash:  "0000000000000000",
		Tenant:      "team",
		Environment: "dev",
		Version:     "1.0.0",
	})
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	out := normalize.Apply(result.Combined)
	if !strings.Contains(out, "dedup: reclaim (release=test-context-team-dev-team-devops, marker age ") {
		t.Fatalf("expected aged-marker reclaim trace, got:\n%s", out)
	}
	if !strings.Contains(out, "exceeds max 15m0s)") {
		t.Fatalf("expected max-age fragment in reclaim trace, got:\n%s", out)
	}
	if !strings.Contains(out, "==> Deployed team/dev <VERSION>") {
		t.Fatalf("expected deploy to proceed after reclaim, got:\n%s", out)
	}
}

func testDeployRealRunDedupReplacesUnreadableMarker(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	markerPath := fixture.SeedDeployInflightMarker(t, setup, "test-context", "team-dev", "team-devops", fixture.DeployInflightRecord{
		PID: os.Getpid(),
	})
	if err := os.WriteFile(markerPath, []byte("{"), 0o600); err != nil {
		t.Fatalf("corrupt marker: %v", err)
	}
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/real_run_dedup_replaces_unreadable_marker", normalize.Apply(result.Combined))
}

func testDeployRealRunDedupConflictOnDifferentParams(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDeployInflightMarker(t, setup, "test-context", "team-dev", "team-devops", fixture.DeployInflightRecord{
		PID:         os.Getpid(),
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
		ParamsHash:  "0000000000000000",
		Tenant:      "team",
		Environment: "dev",
		Version:     "1.0.0-other",
	})
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit on conflicting live deploy, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/real_run_dedup_conflict_on_different_params", normalize.Apply(result.Combined))
}

func testDeployRealRunDedupReclaimWhenMarkerPIDDead(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDeployInflightMarker(t, setup, "test-context", "team-dev", "team-devops", fixture.DeployInflightRecord{
		PID:         reapedChildPID(t),
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
		ParamsHash:  "0000000000000000",
		Tenant:      "team",
		Environment: "dev",
		Version:     "1.0.0",
	})
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/real_run_dedup_reclaim_when_marker_pid_dead", normalize.Apply(result.Combined))
}

func testDeployDryRunDedupSkipWhenIdenticalMarkerAlive(t *testing.T) {
	// Our own pid is always alive. The marker must be fresh: the fixture's fixed
	// StartedAt ages past the 15-minute ceiling.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	first := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if first.ExitCode != 0 {
		t.Fatalf("first dry-run exited %d:\n%s", first.ExitCode, first.Combined)
	}
	hash := extractDedupHash(t, first.Combined)
	fixture.SeedDeployInflightMarker(t, setup, "test-context", "team-dev", "team-devops", fixture.DeployInflightRecord{
		PID:         os.Getpid(),
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
		ParamsHash:  hash,
		Tenant:      "team",
		Environment: "dev",
		Version:     "1.0.0",
	})
	second := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if second.ExitCode != 0 {
		t.Fatalf("second dry-run exited %d:\n%s", second.ExitCode, second.Combined)
	}
	out := normalize.Apply(second.Combined)
	if !strings.Contains(out, "dedup: would skip") {
		t.Fatalf("expected 'dedup: would skip' trace, got:\n%s", out)
	}
	if !strings.Contains(out, "==> Skipping team/dev <VERSION> (identical deploy already in progress)") {
		t.Fatalf("expected ==> Skipping info line, got:\n%s", out)
	}
}

func testDeployDryRunDedupConflictOnDifferentParams(t *testing.T) {
	// The marker must be fresh or the max-age reclaim fires first.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDeployInflightMarker(t, setup, "test-context", "team-dev", "team-devops", fixture.DeployInflightRecord{
		PID:         os.Getpid(),
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
		ParamsHash:  "0000000000000000",
		Tenant:      "team",
		Environment: "dev",
		Version:     "1.0.0-other",
	})
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit on conflicting deploy, got 0:\n%s", result.Combined)
	}
	out := normalize.Apply(result.Combined)
	if !strings.Contains(out, "another erun deploy is in progress") {
		t.Fatalf("expected concurrent-deploy error, got:\n%s", out)
	}
	if !strings.Contains(out, "release \"team-devops\"") {
		t.Fatalf("expected release pointer in error, got:\n%s", out)
	}
}

func testDeployDryRunDedupReclaimWhenMarkerPIDDead(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	// PID 0 short-circuits isProcessAlive; a reaped PID exercises the darwin
	// os.ErrProcessDone vs linux ESRCH path.
	deadPID := reapedChildPID(t)
	fixture.SeedDeployInflightMarker(t, setup, "test-context", "team-dev", "team-devops", fixture.DeployInflightRecord{
		PID:         deadPID,
		ParamsHash:  "0000000000000000",
		Tenant:      "team",
		Environment: "dev",
		Version:     "1.0.0",
	})
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("dry-run exited %d (expected 0 since prior pid is dead):\n%s", result.ExitCode, result.Combined)
	}
	out := normalize.Apply(result.Combined)
	if !strings.Contains(out, "dedup: would reclaim") {
		t.Fatalf("expected 'dedup: would reclaim' trace, got:\n%s", out)
	}
}

// extractDedupHash reads the hash from raw (pre-normalization) -vv dry-run output.
func extractDedupHash(t *testing.T, raw string) string {
	t.Helper()
	const marker = "dedup: ready ("
	idx := strings.Index(raw, marker)
	if idx < 0 {
		t.Fatalf("dedup-ready trace not found in:\n%s", raw)
	}
	rest := raw[idx:]
	hashIdx := strings.Index(rest, "hash=")
	if hashIdx < 0 {
		t.Fatalf("hash= field not found in dedup trace:\n%s", rest)
	}
	hashStart := hashIdx + len("hash=")
	end := hashStart
	for end < len(rest) && (isHex(rest[end])) {
		end++
	}
	if end == hashStart {
		t.Fatalf("could not parse hash from dedup trace:\n%s", rest)
	}
	return rest[hashStart:end]
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func reapedChildPID(t *testing.T) int {
	t.Helper()
	// Windows recycles PIDs aggressively, so a reaped PID may read as alive.
	if runtime.GOOS == "windows" {
		t.Skip("reaped-pid reclaim relies on Unix signal liveness; Windows PID reuse is non-deterministic")
	}
	cmd := harnessexec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatalf("seed reaped child: %v", err)
	}
	pid := cmd.ProcessState.Pid()
	if pid <= 0 {
		t.Fatalf("seed reaped child returned invalid pid %d", pid)
	}
	return pid
}
