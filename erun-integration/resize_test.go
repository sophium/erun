package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestResize(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"resize", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "resize/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_explicit_values", func(t *testing.T) {
		// --dry-run must trace the resolved plan (current -> target per
		// resource) and the note on what moves/doesn't, without writing the
		// env config or rolling anything.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "6", "--memory", "12288Mi", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "resize/dry_run_explicit_values", normalize.Apply(result.Combined))
		assertEnvConfigUnchanged(t, setup, "team", "dev")
	})

	t.Run("dry_run_no_op_when_already_sized", func(t *testing.T) {
		// The default runtimepod is cpu=4 memory=8916Mi (runtime_resources.go);
		// asking for exactly that must report a no-op and never reach the
		// lease check or the deploy composition.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "4", "--memory", "8916Mi", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "resize/dry_run_no_op_when_already_sized", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_apply_recommendation_without_history_refuses", func(t *testing.T) {
		// A host-side invocation has no retained usage history to read (it is
		// only ever written in-pod), so --apply-recommendation must refuse
		// with an explicit, actionable message rather than silently sizing
		// from nothing.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--apply-recommendation", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit with no retained usage history, got 0: %s", result.Combined)
		}
		golden.Equal(t, "resize/dry_run_apply_recommendation_without_history_refuses", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_conflicting_inputs_refuses", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "6", "--apply-recommendation", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for conflicting inputs, got 0: %s", result.Combined)
		}
		golden.Equal(t, "resize/dry_run_conflicting_inputs_refuses", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_no_target_given_refuses", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit with no target given, got 0: %s", result.Combined)
		}
		golden.Equal(t, "resize/dry_run_no_target_given_refuses", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_exceeds_namespace_quota_refuses", func(t *testing.T) {
		// The namespace quota is 10 CPU; the erun-dind sidecar's own fixed 4
		// CPU is spent before the runtime container gets anything, so a
		// requested 8 (8+4=12) must be refused naming the resource, the
		// sidecar's share, and what is actually available (6).
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		appendEnvConfigForTest(t, setup, "team", "dev", "namespacequota:\n  cpu: \"10\"\n  memory: 32Gi\n  storage: 80Gi\n")
		result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "8", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit exceeding the namespace quota, got 0: %s", result.Combined)
		}
		golden.Equal(t, "resize/dry_run_exceeds_namespace_quota_refuses", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_held_lease_refuses_and_names_holder", func(t *testing.T) {
		// The lease-held refusal is the safety property the resize command
		// exists to enforce: staging a held exclusive lease (an orchestrator
		// mid-build) must refuse the resize and name the holder, even under
		// --dry-run, since dry-run must show every decision including this one.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedHeldExclusiveLease(t, setup, "team", "dev", "eng-42", "exec_job_attach")
		result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "6", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit while a lease is held, got 0: %s", result.Combined)
		}
		if !strings.Contains(result.Combined, "orchestrator eng-42") {
			t.Fatalf("expected the refusal to name the holder, got:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "exec_job_attach") {
			t.Fatalf("expected the refusal to name the held lease, got:\n%s", result.Combined)
		}
		golden.Equal(t, "resize/dry_run_held_lease_refuses_and_names_holder", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_held_lease_with_override_proceeds", func(t *testing.T) {
		// The override is explicit and is recorded: passing --override-lease
		// against the same held lease must let the dry-run plan through, and
		// the trace must say the override happened rather than staying silent
		// about it.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedHeldExclusiveLease(t, setup, "team", "dev", "eng-42", "exec_job_attach")
		result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "6", "--override-lease", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "overriding") {
			t.Fatalf("expected the trace to record the override, got:\n%s", result.Combined)
		}
		golden.Equal(t, "resize/dry_run_held_lease_with_override_proceeds", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_apply_recommendation_traces_the_evidence", func(t *testing.T) {
		// The recommendation resize resolves its target from must not stay
		// invisible: the trace must show the same verdict/evidence reasoning
		// `erun list` prints under `runtime-pod:`, so a caller sees not just
		// what changes but why. This reuses list_test.go's shrink-eligible
		// fixture (a long, quiet window comfortably under the limit).
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedUsageHistory(t, setup, "team", "dev", usageHistorySpec{
			windowHours: 31, samples: 240,
			peakMemoryBytes: 12742377472, limitBytes: 24696061952,
			quotaMilli: 12000, periods: 376556, throttled: 0, peakCPUMilli: 4567,
		})
		result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--apply-recommendation", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "sizing-evidence: 31h12m observed") {
			t.Fatalf("expected the trace to carry the evidence line, got:\n%s", result.Combined)
		}
		golden.Equal(t, "resize/dry_run_apply_recommendation_traces_the_evidence", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_apply_recommendation_no_op_still_traces_why", func(t *testing.T) {
		// The exact case the recommendation's evidence has to answer: a
		// no-op that reports "already sized" must not go silent about why --
		// here a comfortable peak that hasn't been watched long enough for
		// erun to trust a shrink (short window, `insufficient-evidence`),
		// which resolves to no action at all.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedUsageHistory(t, setup, "team", "dev", usageHistorySpec{
			windowHours: 1, samples: 120,
			peakMemoryBytes: 12742377472, limitBytes: 24696061952,
			quotaMilli: 12000, periods: 376556, throttled: 0, peakCPUMilli: 4567,
		})
		result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--apply-recommendation", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "insufficient-evidence") {
			t.Fatalf("expected the trace to name the unmet gate, got:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "already sized") {
			t.Fatalf("expected the plan to resolve to a no-op, got:\n%s", result.Combined)
		}
		golden.Equal(t, "resize/dry_run_apply_recommendation_no_op_still_traces_why", normalize.Apply(result.Combined))
	})

	t.Run("real_run_persists_and_redeploys_via_stubs", func(t *testing.T) {
		// Drives the real (non-dry-run) path: persist EnvConfig.runtimepod,
		// take-and-release the resize's own exclusive lease, then roll the
		// pod through the same deploy composition `erun deploy` uses.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "6", "--memory", "12288Mi"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "resize/real_run_persists_and_redeploys_via_stubs", normalize.Apply(result.Combined))

		data, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml"))
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		if !strings.Contains(string(data), "cpu: \"6\"") && !strings.Contains(string(data), "cpu: 6") {
			t.Fatalf("expected the persisted runtimepod cpu to be 6, got:\n%s", data)
		}
		if !strings.Contains(string(data), "12288Mi") {
			t.Fatalf("expected the persisted runtimepod memory to be 12288Mi, got:\n%s", data)
		}
	})
}

// assertEnvConfigUnchanged guards the dry-run no-write contract: the env
// config on disk must not carry the resize's target values.
func assertEnvConfigUnchanged(t testing.TB, setup env.Setup, tenant, environment string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", tenant, environment, "config.yaml"))
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	if strings.Contains(string(data), "runtimepod") {
		t.Fatalf("expected --dry-run to leave the env config unwritten, got:\n%s", data)
	}
}

// appendEnvConfigForTest mirrors the package-private fixture.appendEnvConfig,
// duplicated here because that helper is not exported across the fixture
// package boundary and resize is the first scenario file outside fixture.go
// needing a namespace quota on an otherwise-default SeedTenantEnv env.
func appendEnvConfigForTest(t testing.TB, setup env.Setup, tenant, environment, extra string) {
	t.Helper()
	path := filepath.Join(setup.ConfigHome, "erun", tenant, environment, "config.yaml")
	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read env config %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(existing, []byte(extra)...), 0o644); err != nil {
		t.Fatalf("write env config %s: %v", path, err)
	}
}

// seedHeldExclusiveLease writes an exclusive activity lease file directly
// into the isolated cache root, bypassing the real `erun activity lease take`
// command: this test only needs a lease to already be held when `resize`
// runs, not to exercise the take path itself. Mirrors the on-disk shape
// erun-common/activity_lease.go's takeExclusiveEnvironmentActivityLease
// writes (exclusiveEnvironmentActivityLeaseDir, keyed by sanitized scope).
func seedHeldExclusiveLease(t testing.TB, setup env.Setup, tenant, environment, orchestrator, name string) {
	t.Helper()
	dir := filepath.Join(setup.CacheHome, "erun", "activity", tenant, environment, "leases", "exclusive")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir exclusive lease dir: %v", err)
	}
	lease := `{
  "id": "` + name + `",
  "name": "` + name + `",
  "startedAt": "2030-01-01T00:00:00Z",
  "expiresAt": "2030-01-01T01:00:00Z",
  "scope": "worktree",
  "exclusive": true,
  "holder": {"orchestrator": "` + orchestrator + `"}
}`
	if err := os.WriteFile(filepath.Join(dir, "worktree.json"), []byte(lease), 0o644); err != nil {
		t.Fatalf("write exclusive lease: %v", err)
	}
}
