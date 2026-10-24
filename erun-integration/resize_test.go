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

// TestResize runs in-environment, so the occupancy check reads the local lease store;
// TestResizeOffEnvironment covers dispatch to a remote edge.
func TestResize(t *testing.T) {
	t.Parallel()
	t.Run("help", testResizeHelp)
	t.Run("dry_run_missing_tenant", testResizeDryRunMissingTenant)
	t.Run("dry_run_explicit_values", testResizeDryRunExplicitValues)
	t.Run("dry_run_no_op_when_already_sized", testResizeDryRunNoOpWhenAlreadySized)
	t.Run("dry_run_apply_recommendation_without_history_refuses", testResizeDryRunApplyRecommendationWithoutHistoryRefuses)
	t.Run("dry_run_conflicting_inputs_refuses", testResizeDryRunConflictingInputsRefuses)
	t.Run("dry_run_no_target_given_refuses", testResizeDryRunNoTargetGivenRefuses)
	t.Run("dry_run_exceeds_namespace_quota_refuses", testResizeDryRunExceedsNamespaceQuotaRefuses)
	t.Run("dry_run_dind_explicit_values", testResizeDryRunDindExplicitValues)
	t.Run("dry_run_dind_combined_with_runtime_pod_values", testResizeDryRunDindCombinedWithRuntimePodValues)
	t.Run("dry_run_dind_combined_with_apply_recommendation", testResizeDryRunDindCombinedWithApplyRecommendation)
	t.Run("dry_run_dind_exceeds_namespace_quota_refuses", testResizeDryRunDindExceedsNamespaceQuotaRefuses)
	t.Run("real_run_persists_dind_and_redeploys_via_stubs", testResizeRealRunPersistsDindAndRedeploysViaStubs)
	t.Run("dry_run_held_lease_refuses_and_names_holder", testResizeDryRunHeldLeaseRefusesAndNamesHolder)
	t.Run("dry_run_held_lease_with_override_proceeds", testResizeDryRunHeldLeaseWithOverrideProceeds)
	t.Run("dry_run_apply_recommendation_traces_the_evidence", testResizeDryRunApplyRecommendationTracesTheEvidence)
	t.Run("dry_run_apply_recommendation_holds_cpu_at_live_limit", testResizeDryRunApplyRecommendationHoldsCPUAtLiveLimit)
	t.Run("dry_run_apply_recommendation_no_op_still_traces_why", testResizeDryRunApplyRecommendationNoOpStillTracesWhy)
	t.Run("real_run_persists_and_redeploys_via_stubs", testResizeRealRunPersistsAndRedeploysViaStubs)
}

func testResizeHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"resize", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "resize/help", normalize.Apply(result.Combined))
}

func testResizeDryRunMissingTenant(t *testing.T) {
	// The no-tenant failure must name resize's own --tenant recovery.
	setup := env.New(t)
	result := erun.Run(t, []string{"resize", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit with no tenant configured, got 0: %s", result.Combined)
	}
	golden.Equal(t, "resize/dry_run_missing_tenant", normalize.Apply(result.Combined))
}

func testResizeDryRunExplicitValues(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "6", "--memory", "20480Mi", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "resize/dry_run_explicit_values", normalize.Apply(result.Combined))
	assertEnvConfigUnchanged(t, setup, "team", "dev")
}

func testResizeDryRunNoOpWhenAlreadySized(t *testing.T) {
	// The default runtimepod (cpu=4, 16384Mi) is a no-op that skips the lease check and deploy.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "4", "--memory", "16384Mi", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "resize/dry_run_no_op_when_already_sized", normalize.Apply(result.Combined))
}

func testResizeDryRunApplyRecommendationWithoutHistoryRefuses(t *testing.T) {
	// Usage history is only written in-pod, so host-side --apply-recommendation refuses.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--apply-recommendation", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit with no retained usage history, got 0: %s", result.Combined)
	}
	golden.Equal(t, "resize/dry_run_apply_recommendation_without_history_refuses", normalize.Apply(result.Combined))
}

func testResizeDryRunConflictingInputsRefuses(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "6", "--apply-recommendation", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for conflicting inputs, got 0: %s", result.Combined)
	}
	golden.Equal(t, "resize/dry_run_conflicting_inputs_refuses", normalize.Apply(result.Combined))
}

func testResizeDryRunNoTargetGivenRefuses(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit with no target given, got 0: %s", result.Combined)
	}
	golden.Equal(t, "resize/dry_run_no_target_given_refuses", normalize.Apply(result.Combined))
}

func testResizeDryRunExceedsNamespaceQuotaRefuses(t *testing.T) {
	// Quota is 10 CPU and the dind sidecar takes 4, so 8 is refused with 6 available.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	appendEnvConfigForTest(t, setup, "team", "dev", "namespacequota:\n  cpu: \"10\"\n  memory: 32Gi\n  storage: 80Gi\n")
	result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "8", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit exceeding the namespace quota, got 0: %s", result.Combined)
	}
	golden.Equal(t, "resize/dry_run_exceeds_namespace_quota_refuses", normalize.Apply(result.Combined))
}

func testResizeDryRunDindExplicitValues(t *testing.T) {
	// Sidecar flags resize only the sidecar; the runtime pod keeps its defaults.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--dind-cpu", "6", "--dind-memory", "16Gi", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "resize/dry_run_dind_explicit_values", normalize.Apply(result.Combined))
	assertEnvConfigUnchanged(t, setup, "team", "dev")
}

func testResizeDryRunDindCombinedWithRuntimePodValues(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "6", "--dind-memory", "16Gi", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "resize/dry_run_dind_combined_with_runtime_pod_values", normalize.Apply(result.Combined))
}

func testResizeDryRunDindCombinedWithApplyRecommendation(t *testing.T) {
	// Unlike explicit --cpu/--memory, --apply-recommendation combines with the dind flags.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedUsageHistory(t, setup, "team", "dev", usageHistorySpec{
		windowHours: 31, samples: 240,
		peakMemoryBytes: 12742377472, limitBytes: 24696061952,
		quotaMilli: 12000, periods: 376556, throttled: 0, peakCPUMilli: 4567,
	})
	result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--apply-recommendation", "--dind-memory", "16Gi", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "resize/dry_run_dind_combined_with_apply_recommendation", normalize.Apply(result.Combined))
}

func testResizeDryRunDindExceedsNamespaceQuotaRefuses(t *testing.T) {
	// The sidecar's requested target counts against the namespace quota too.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	appendEnvConfigForTest(t, setup, "team", "dev", "namespacequota:\n  cpu: \"10\"\n  memory: 32Gi\n  storage: 80Gi\n")
	result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--dind-cpu", "8", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit exceeding the namespace quota, got 0: %s", result.Combined)
	}
	golden.Equal(t, "resize/dry_run_dind_exceeds_namespace_quota_refuses", normalize.Apply(result.Combined))
}

func testResizeRealRunPersistsDindAndRedeploysViaStubs(t *testing.T) {
	// The runtime pod is re-persisted at its unchanged default, proving the knobs are independent.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(inEnvironment(setup.Env()), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--dind-cpu", "6", "--dind-memory", "16384Mi"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "resize/real_run_persists_dind_and_redeploys_via_stubs", normalize.Apply(result.Combined))

	data, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml"))
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	if !strings.Contains(string(data), "runtimedindpod") {
		t.Fatalf("expected the persisted config to carry runtimedindpod, got:\n%s", data)
	}
	if !strings.Contains(string(data), "cpu: \"6\"") && !strings.Contains(string(data), "cpu: 6") {
		t.Fatalf("expected the persisted runtimedindpod cpu to be 6, got:\n%s", data)
	}
	if !strings.Contains(string(data), "16384Mi") {
		t.Fatalf("expected the persisted runtimedindpod memory to be 16384Mi, got:\n%s", data)
	}
	if !strings.Contains(string(data), "runtimepod:\n    cpu: \"4\"\n    memory: 16384Mi") {
		t.Fatalf("expected the runtime pod's own resources to stay at their unchanged default, got:\n%s", data)
	}
}

func testResizeDryRunHeldLeaseRefusesAndNamesHolder(t *testing.T) {
	// A held exclusive lease refuses the resize and names the holder, even under --dry-run.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedHeldExclusiveLease(t, setup, "team", "dev", "eng-42", "exec_job_attach")
	result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "6", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
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
}

func testResizeDryRunHeldLeaseWithOverrideProceeds(t *testing.T) {
	// --override-lease lets the plan through and the trace records the override.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedHeldExclusiveLease(t, setup, "team", "dev", "eng-42", "exec_job_attach")
	result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "6", "--override-lease", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "overriding") {
		t.Fatalf("expected the trace to record the override, got:\n%s", result.Combined)
	}
	golden.Equal(t, "resize/dry_run_held_lease_with_override_proceeds", normalize.Apply(result.Combined))
}

func testResizeDryRunApplyRecommendationTracesTheEvidence(t *testing.T) {
	// The trace shows the same verdict and evidence as `erun list`'s `runtime-pod:`.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedUsageHistory(t, setup, "team", "dev", usageHistorySpec{
		windowHours: 31, samples: 240,
		peakMemoryBytes: 12742377472, limitBytes: 24696061952,
		quotaMilli: 12000, periods: 376556, throttled: 0, peakCPUMilli: 4567,
	})
	result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--apply-recommendation", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "sizing-evidence: 31h12m observed") {
		t.Fatalf("expected the trace to carry the evidence line, got:\n%s", result.Combined)
	}
	golden.Equal(t, "resize/dry_run_apply_recommendation_traces_the_evidence", normalize.Apply(result.Combined))
}

func testResizeDryRunApplyRecommendationHoldsCPUAtLiveLimit(t *testing.T) {
	// The plan uses the live cgroup limits (12 CPU / 23552Mi), not the silent config's
	// defaults. Leading spaces keep needles off the dind-cpu/dind-memory fields.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedUsageHistory(t, setup, "team", "dev", usageHistorySpec{
		windowHours: 48, samples: 240,
		peakMemoryBytes: 7751073792, limitBytes: 24696061952,
		quotaMilli: 12000, periods: 900598, throttled: 13, peakCPUMilli: 4567,
	})
	result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--apply-recommendation", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "cpu hold from 12") {
		t.Fatalf("expected the verdict to hold cpu at the live 12, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "memory 23552Mi -> 11264Mi") {
		t.Fatalf("expected the plan to read memory's live 23552Mi as its from value, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "cpu=12") {
		t.Fatalf("expected the resolved command to carry cpu=12 (the live value) through unchanged, got:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, " cpu=4") || strings.Contains(result.Combined, " memory=16384Mi") {
		t.Fatalf("expected the plan to never fall back to the package defaults cpu=4/16384Mi, got:\n%s", result.Combined)
	}
	golden.Equal(t, "resize/dry_run_apply_recommendation_holds_cpu_at_live_limit", normalize.Apply(result.Combined))
}

func testResizeDryRunApplyRecommendationNoOpStillTracesWhy(t *testing.T) {
	// An "already sized" no-op must still say why (insufficient-evidence here).
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedUsageHistory(t, setup, "team", "dev", usageHistorySpec{
		windowHours: 1, samples: 120,
		peakMemoryBytes: 12742377472, limitBytes: 24696061952,
		quotaMilli: 12000, periods: 376556, throttled: 0, peakCPUMilli: 4567,
	})
	result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--apply-recommendation", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
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
}

func testResizeRealRunPersistsAndRedeploysViaStubs(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(inEnvironment(setup.Env()), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "6", "--memory", "20480Mi"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
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
	if !strings.Contains(string(data), "20480Mi") {
		t.Fatalf("expected the persisted runtimepod memory to be 20480Mi, got:\n%s", data)
	}
}

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

// appendEnvConfigForTest duplicates fixture.appendEnvConfig, which is not exported.
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

// seedHeldExclusiveLease writes the lease file in the on-disk shape
// takeExclusiveEnvironmentActivityLease uses, bypassing `erun activity lease take`.
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
