package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// The working-hours window decides the stop-eligibility arm, so these pin it:
// 00:00-23:59 is always inside (HH:MM cannot spell 24:00) and 23:59-00:00 is
// inside only during the 23:59 UTC minute.
const (
	insideWorkingHoursIdleBlock  = "idle:\n  workinghours: 00:00-23:59\n  timezone: UTC\n"
	outsideWorkingHoursIdleBlock = "idle:\n  workinghours: 23:59-00:00\n  timezone: UTC\n"
)

func seedManagedCloudTenantEnv(t *testing.T, setup env.Setup, tenant, environment string) {
	t.Helper()
	seedManagedCloudTenantEnvWithIdle(t, setup, tenant, environment, "")
}

func seedManagedCloudTenantEnvWithIdle(t *testing.T, setup env.Setup, tenant, environment, idleBlock string) {
	t.Helper()
	fixture.SeedTenantEnv(t, setup, tenant, environment)
	cfgPath := filepath.Join(setup.ConfigHome, "erun", tenant, environment, "config.yaml")
	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read env config %s: %v", cfgPath, err)
	}
	body = append(body, []byte("managedcloud: true\n")...)
	body = append(body, []byte(idleBlock)...)
	if err := os.WriteFile(cfgPath, body, 0o644); err != nil {
		t.Fatalf("write env config %s: %v", cfgPath, err)
	}
}

func seedStopPending(t *testing.T, home, tenant, environment, body string) string {
	t.Helper()
	dir := filepath.Join(home, ".erun", tenant, environment)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, "stop-pending.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func seedActivitySnapshot(t *testing.T, cacheHome, tenant, environment, kind, body string) {
	t.Helper()
	dir := filepath.Join(cacheHome, "erun", "activity", tenant, environment)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, kind+".json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestActivity(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"touch_records_cli_activity", testActivityTouchRecordsCLIActivity},
		{"status_with_seeded_env", testActivityStatusWithSeededEnv},
		{"status_json_output", testActivityStatusJSONOutput},
		{"stop_ready_blocks_when_active", testActivityStopReadyBlocksWhenActive},
		{"status_json_includes_per_client_breakdown", testActivityStatusJSONIncludesPerClientBreakdown},
		{"stop_ready_json_emits_structured_decision", testActivityStopReadyJSONEmitsStructuredDecision},
		{"record_stop_folds_state_stdin_into_history", testActivityRecordStopFoldsStateStdinIntoHistory},
		{"record_stop_host_manual_without_pending", testActivityRecordStopHostManualWithoutPending},
		{"stop_ready_arms_grace_for_managed_cloud_env", testActivityStopReadyArmsGraceForManagedCloudEnv},
		{"stop_ready_waits_while_grace_window_open", testActivityStopReadyWaitsWhileGraceWindowOpen},
		{"stop_ready_fires_after_grace_elapsed", testActivityStopReadyFiresAfterGraceElapsed},
		{"stop_ready_skip_clears_stale_pending", testActivityStopReadySkipClearsStalePending},
		{"cancel_stop_pending_removes_armed_grace", testActivityCancelStopPendingRemovesArmedGrace},
		{"stop_ready_requires_target", testActivityStopReadyRequiresTarget},
		{"stop_ready_errors_on_corrupt_pending", testActivityStopReadyErrorsOnCorruptPending},
		{"record_stop_errors_on_corrupt_history", testActivityRecordStopErrorsOnCorruptHistory},
		{"cancel_stop_pending_requires_target", testActivityCancelStopPendingRequiresTarget},
		{"record_stop_falls_back_to_on_disk_pending", testActivityRecordStopFallsBackToOnDiskPending},
		{"record_stop_caps_history_at_ten", testActivityRecordStopCapsHistoryAtTen},
		{"touch_rejects_unknown_kind", testActivityTouchRejectsUnknownKind},
		{"touch_evicts_oldest_client_beyond_cap", testActivityTouchEvictsOldestClientBeyondCap},
		{"status_distinguishes_stale_and_codex_open_markers", testActivityStatusDistinguishesStaleAndCodexOpenMarkers},
		{"lease_help", testActivityLeaseHelp},
		{"lease_take_help_names_what_an_exclusive_claim_actually_refuses", testActivityLeaseTakeHelpNamesWhatAnExclusiveClaimActuallyRefuses},
		{"lease_take_list_and_release", testActivityLeaseTakeListAndRelease},
		{"lease_take_json_records_the_holder_process", testActivityLeaseTakeJSONRecordsTheHolderProcess},
		{"lease_take_requires_a_name", testActivityLeaseTakeRequiresAName},
		{"lease_take_exclusive_dry_run", testActivityLeaseTakeExclusiveDryRun},
		{"lease_take_exclusive_refuses_a_second_holder_and_names_it", testActivityLeaseTakeExclusiveRefusesASecondHolderAndNamesIt},
		{"lease_take_exclusive_allows_a_different_scope_to_coexist", testActivityLeaseTakeExclusiveAllowsADifferentScopeToCoexist},
		{"lease_release_exclusive_round_trip", testActivityLeaseReleaseExclusiveRoundTrip},
		{"lease_release_reports_not_held_for_an_unknown_id", testActivityLeaseReleaseReportsNotHeldForAnUnknownID},
		{"lease_release_exclusive_wrong_scope_reports_not_held", testActivityLeaseReleaseExclusiveWrongScopeReportsNotHeld},
		{"lease_release_exclusive_held_by_another_id_names_the_holder", testActivityLeaseReleaseExclusiveHeldByAnotherIDNamesTheHolder},
		{"stop_ready_blocked_by_a_held_lease", testActivityStopReadyBlockedByAHeldLease},
		{"stop_ready_blocked_by_a_held_lease_outside_working_hours", testActivityStopReadyBlockedByAHeldLeaseOutsideWorkingHours},
		{"sample_records_activity_only_when_work_advances", testActivitySampleRecordsActivityOnlyWhenWorkAdvances},
		{"sample_records_ssh_activity_for_a_pty_holding_session", testActivitySampleRecordsSSHActivityForAPtyHoldingSession},
		{"sample_ignores_a_notty_ssh_child", testActivitySampleIgnoresANottySSHChild},
		{"sample_retains_runtime_usage_from_cgroup_counters", testActivitySampleRetainsRuntimeUsageFromCgroupCounters},
		{"sample_retains_the_peak_across_a_container_restart", testActivitySampleRetainsThePeakAcrossAContainerRestart},
		{"sample_records_no_usage_when_the_host_supplies_no_counters", testActivitySampleRecordsNoUsageWhenTheHostSuppliesNoCounters},
		{"sample_requires_target", testActivitySampleRequiresTarget},
		{"ssh_proxy_requires_tenant_and_environment", testActivitySSHProxyRequiresTenantAndEnvironment},
		{"ssh_proxy_requires_listen_and_target", testActivitySSHProxyRequiresListenAndTarget},
		{"ssh_proxy_rejects_negative_idle_traffic_threshold", testActivitySSHProxyRejectsNegativeIdleTrafficThreshold},
		{"ssh_proxy_rejects_unlistenable_address", testActivitySSHProxyRejectsUnlistenableAddress},
	} {
		t.Run(scenario.name, scenario.run)
	}
}

func testActivityTouchRecordsCLIActivity(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"activity", "touch", "--tenant", "team", "--environment", "dev", "--kind", "cli"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "activity/touch_records_cli_activity", normalize.Apply(result.Combined))
}

func testActivityStatusWithSeededEnv(t *testing.T) {
	// The working-hours line varies by wall clock, so it is asserted structurally.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"activity", "status", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	stableLines := []string{
		"stop eligible: off",
		"stop blocked: environment is not cloud-managed",
		"ssh: idle (no activity recorded)",
		"api: idle (no activity recorded)",
		"mcp: idle (no activity recorded)",
		"cli: idle (no activity recorded)",
		"codex: idle (no activity recorded)",
	}
	for _, line := range stableLines {
		if !strings.Contains(result.Stdout, line) {
			t.Errorf("expected stdout to contain %q, got:\n%s", line, result.Stdout)
		}
	}
	workingHours := regexp.MustCompile(`(?m)^\s*working-hours: (idle|active) \((inside|outside) working hours \d{2}:\d{2}-\d{2}:\d{2}\)\s*$`)
	if !workingHours.MatchString(result.Stdout) {
		t.Errorf("expected working-hours marker line, got:\n%s", result.Stdout)
	}
}

func testActivityStatusJSONOutput(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"activity", "status", "--tenant", "team", "--environment", "dev", "--json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	stdout := strings.TrimSpace(result.Stdout)
	if !strings.HasPrefix(stdout, "{") || !strings.Contains(stdout, "\"markers\"") {
		t.Errorf("expected JSON object with markers field, got:\n%s", stdout)
	}
}

func testActivityStatusJSONIncludesPerClientBreakdown(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	erun.Run(t, []string{
		"activity", "touch",
		"--tenant", "team",
		"--environment", "dev",
		"--kind", "ssh",
		"--bytes", "2048",
		"--client-address", "10.0.4.7",
		"--client-bytes", "2048",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	result := erun.Run(t, []string{
		"activity", "status",
		"--tenant", "team",
		"--environment", "dev",
		"--json",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Stdout, `"clients"`) {
		t.Errorf("expected per-client breakdown in JSON output, got:\n%s", result.Stdout)
	}
	if !strings.Contains(result.Stdout, `"address": "10.0.4.7"`) {
		t.Errorf("expected client address in JSON output, got:\n%s", result.Stdout)
	}
	if !strings.Contains(result.Stdout, `"bytes": 2048`) {
		t.Errorf("expected client bytes in JSON output, got:\n%s", result.Stdout)
	}
}

func testActivityTouchRejectsUnknownKind(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"activity", "touch", "--tenant", "team", "--environment", "dev", "--kind", "bogus"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for unknown kind, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "activity/touch_rejects_unknown_kind", normalize.Apply(result.Combined))
}

func testActivityTouchEvictsOldestClientBeyondCap(t *testing.T) {
	// Sequential subprocesses make the first-touched client the one evicted at the cap.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	for i := 1; i <= 9; i++ {
		address := fmt.Sprintf("10.0.0.%d", i)
		result := erun.Run(t, []string{"activity", "touch", "--tenant", "team", "--environment", "dev", "--kind", "ssh", "--bytes", "1", "--client-address", address, "--client-bytes", "1"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("touch %s: exit %d: %s", address, result.ExitCode, result.Combined)
		}
	}
	body, err := os.ReadFile(filepath.Join(setup.CacheHome, "erun", "activity", "team", "dev", "ssh.json"))
	if err != nil {
		t.Fatalf("read ssh.json: %v", err)
	}
	var snapshot struct {
		Clients map[string]struct {
			Bytes int64 `json:"bytes"`
		} `json:"clients"`
	}
	if err := json.Unmarshal(body, &snapshot); err != nil {
		t.Fatalf("parse ssh.json: %v", err)
	}
	if len(snapshot.Clients) != 8 {
		t.Fatalf("expected clients capped at 8, got %d:\n%s", len(snapshot.Clients), body)
	}
	if _, ok := snapshot.Clients["10.0.0.1"]; ok {
		t.Errorf("expected oldest client 10.0.0.1 evicted, got:\n%s", body)
	}
	if _, ok := snapshot.Clients["10.0.0.9"]; !ok {
		t.Errorf("expected newest client 10.0.0.9 retained, got:\n%s", body)
	}
}

func testActivityStatusDistinguishesStaleAndCodexOpenMarkers(t *testing.T) {
	// The working-hours line depends on wall clock, so no whole-stream golden.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedActivitySnapshot(t, setup.CacheHome, "team", "dev", "cli", `{"lastActivity": "2026-01-01T08:00:00Z", "lastSeen": "2026-01-01T08:00:00Z"}
`)
	seedActivitySnapshot(t, setup.CacheHome, "team", "dev", "codex", `{"lastActivity": "2026-01-01T08:00:00Z", "lastSeen": "2026-01-01T08:00:00Z"}
`)
	touch := erun.Run(t, []string{"activity", "touch", "--tenant", "team", "--environment", "dev", "--kind", "codex", "--seen"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if touch.ExitCode != 0 {
		t.Fatalf("touch --seen: exit %d: %s", touch.ExitCode, touch.Combined)
	}
	result := erun.Run(t, []string{"activity", "status", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Stdout, "cli: idle (last activity exceeded timeout)") {
		t.Errorf("expected stale cli marker reason, got:\n%s", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "codex: idle (codex is open but idle)") {
		t.Errorf("expected codex open-but-idle reason, got:\n%s", result.Stdout)
	}
}

func testActivitySSHProxyRequiresTenantAndEnvironment(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"activity", "ssh-proxy", "--listen", "127.0.0.1:0", "--target", "127.0.0.1:1"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit without tenant/environment, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "activity/ssh_proxy_requires_tenant_and_environment", normalize.Apply(result.Combined))
}

func testActivitySSHProxyRequiresListenAndTarget(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"activity", "ssh-proxy", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit without addresses, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "activity/ssh_proxy_requires_listen_and_target", normalize.Apply(result.Combined))
}

func testActivitySSHProxyRejectsNegativeIdleTrafficThreshold(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"activity", "ssh-proxy", "--tenant", "team", "--environment", "dev", "--listen", "127.0.0.1:0", "--target", "127.0.0.1:1", "--idle-traffic-bytes=-1"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for negative threshold, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "activity/ssh_proxy_rejects_negative_idle_traffic_threshold", normalize.Apply(result.Combined))
}

func testActivitySSHProxyRejectsUnlistenableAddress(t *testing.T) {
	// A port-less address fails listener setup deterministically.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"activity", "ssh-proxy", "--tenant", "team", "--environment", "dev", "--listen", "127.0.0.1", "--target", "127.0.0.1:1"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for unlistenable address, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "activity/ssh_proxy_rejects_unlistenable_address", normalize.Apply(result.Combined))
}
