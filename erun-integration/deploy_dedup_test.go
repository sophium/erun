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
	// Real-run arm of the singleflight skip: a live marker (our own pid)
	// with the identical params hash and a fresh StartedAt makes the
	// second deploy a no-op success without ever invoking helm. The hash
	// comes from a first dry-run against the same resolved spec; the
	// real run must use -vv because the helm argv (and therefore the
	// params hash) embeds verbosity flags and dry-run always traces at
	// raised verbosity — a verbosity-0 real run computes a hash the
	// dry-run probe never reports. The marker must also be recent or the
	// max-age reclaim arm would fire first.
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
	// Real-run max-age reclaim arm: the marker's pid is alive (our own),
	// but its StartedAt (the fixture's fixed 2026-05-10 default) is far
	// past the 15-minute deploy ceiling, so the deploy reclaims the
	// marker and proceeds. The reclaim trace embeds the marker's current
	// wall-clock age, which no normalization rule can pin down, so this
	// scenario asserts the stable fragments instead of a whole-stream
	// golden.
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
	// Real-run unreadable-marker arm: a corrupt marker (truncated JSON)
	// must not block the deploy — the runner traces the replacement,
	// removes the file, claims a fresh marker, and proceeds.
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
	// Real-run conflict arm: a live, fresh marker with a different params
	// hash means another deploy with different intent owns the release;
	// the second invocation must fail fast with
	// HelmReleaseConcurrentDeployError instead of stomping the live helm
	// upgrade.
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
	// Real-run dead-pid reclaim arm: a fresh marker whose recorded pid is
	// already reaped is leftover state from a crashed deploy; the runner
	// reclaims it and proceeds with the rollout.
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
	// When another erun deploy is in flight against the same release with
	// the same params hash, dry-run reports "would skip" and exits 0.
	// We seed the marker with our own pid (always alive during this
	// test) and the params hash erun --dry-run will compute on the
	// first run. The second run sees the live identical marker. The
	// marker must also be recent or the max-age reclaim arm would fire
	// first (the fixture's default StartedAt is fixed, so it ages past
	// the 15-minute ceiling as real time passes).
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
	// A live in-flight deploy with a different params hash should fail
	// the second invocation with HelmReleaseConcurrentDeployError so two
	// callers with conflicting intent surface the conflict instead of
	// stomping on each other's helm release. The marker must also be
	// recent or the max-age reclaim arm would fire first (the fixture's
	// default StartedAt is fixed, so it ages past the 15-minute ceiling
	// as real time passes).
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
	// A leftover marker whose pid is no longer running should not block
	// a fresh dry-run; it reports "would reclaim" so the user can see
	// the stale-state recovery path was taken.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	// Use a real reaped child PID (positive, dead) instead of PID 0.
	// PID 0 short-circuits in isProcessAlive without ever calling
	// Signal(0); a reaped PID forces the live signal-error path that
	// surfaces darwin's os.ErrProcessDone vs. linux's ESRCH. Without
	// that distinction the marker stays "alive" on darwin and deploy
	// is locked out for the full 15-minute max-age fallback.
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

// extractDedupHash pulls the live params hash off a "dedup: ready (release=..., hash=<HEX>)"
// line emitted by erun deploy --dry-run -vv. Tests use this to seed an
// identical-hash marker for the dedup-skip path. The raw output is captured
// before normalization so the hash is the real 16-char hex value, not the
// <HASH> placeholder.
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

// reapedChildPID returns the PID of an already-exited child, so isProcessAlive
// hits the real signal(0) error path (ESRCH on linux, os.ErrProcessDone on
// darwin). PID reuse between the wait and the marker read is theoretically
// possible but vanishingly unlikely.
func reapedChildPID(t *testing.T) int {
	t.Helper()
	// The reaped-pid reclaim path is Unix liveness semantics (Signal(0) →
	// ESRCH/os.ErrProcessDone). Windows checks liveness via the process handle
	// and recycles PIDs aggressively, so a freshly reaped PID reads as
	// non-deterministically alive — the reclaim behaviour itself is covered on
	// Unix, so skip the seeding here rather than ship a flaky test.
	if runtime.GOOS == "windows" {
		t.Skip("reaped-pid reclaim relies on Unix signal liveness; Windows PID reuse is non-deterministic")
	}
	// Spawn and reap a real child to get a positive, dead PID.
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
