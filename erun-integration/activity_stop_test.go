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

func testActivityStopReadyBlocksWhenActive(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	erun.Run(t, []string{"activity", "touch", "--tenant", "team", "--environment", "dev", "--kind", "cli"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	result := erun.Run(t, []string{"activity", "stop-ready", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "activity/stop_ready_blocks_when_active", normalize.Apply(result.Combined))
}

func testActivityStopReadyJSONEmitsStructuredDecision(t *testing.T) {
	// The runtime entrypoint's idle-monitor bash loop needs the JSON on stdout
	// regardless of the stop-eligible exit code, so it can record a tick even
	// when the env stays active.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	erun.Run(t, []string{"activity", "touch", "--tenant", "team", "--environment", "dev", "--kind", "cli"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	result := erun.Run(t, []string{"activity", "stop-ready", "--json", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if !strings.Contains(result.Stdout, `"stopEligible":false`) {
		t.Errorf("expected stopEligible:false in stdout, got:\n%s", result.Stdout)
	}
	if !strings.Contains(result.Stdout, `"blockedReason":"environment is not cloud-managed"`) {
		t.Errorf("expected blockedReason in stdout, got:\n%s", result.Stdout)
	}
	if result.ExitCode == 0 {
		t.Errorf("expected non-zero exit for blocked env, got 0:\n%s", result.Combined)
	}
}

func testActivityRecordStopFoldsStateStdinIntoHistory(t *testing.T) {
	// The in-pod monitor pipes the stop-ready --json blob into record-stop
	// because the Fire branch already cleared stop-pending.json by the time
	// record-stop runs, so the state must round-trip through stdin for the
	// History tab to answer "what triggered it?"
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stdin := `{
  "stopEligible": true,
  "action": "fire",
  "graceSeconds": 600,
  "reasonSummary": "idle: terminal-stdin, ai",
  "pendingState": {
    "since": "2026-05-31T12:20:00Z",
    "graceSeconds": 600,
    "cloudContextName": "mock-cluster",
    "reasonSummary": "idle: terminal-stdin, ai",
    "policy": {
      "timeout": 600000000000,
      "workingHours": "09:00-18:00",
      "timezone": "Europe/Riga",
      "idleTrafficBytes": 0
    },
    "markers": [
      {"name": "terminal-stdin", "idle": true, "reason": "no input"},
      {"name": "ai", "idle": true, "reason": "no Claude session"}
    ]
  }
}`
	result := erun.Run(t, []string{
		"activity", "record-stop",
		"--tenant", "team",
		"--environment", "dev",
		"--source", "pod-monitor",
		"--state-stdin",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: stdin})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	body, err := os.ReadFile(filepath.Join(setup.Home, ".erun", "team", "dev", "stop-history.json"))
	if err != nil {
		t.Fatalf("read stop-history.json: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, `"source": "pod-monitor"`) {
		t.Errorf("expected source=pod-monitor in history, got:\n%s", text)
	}
	if !strings.Contains(text, `"graceSeconds": 600`) {
		t.Errorf("expected graceSeconds=600 carried through stdin, got:\n%s", text)
	}
	if !strings.Contains(text, `"reason": "idle: terminal-stdin, ai"`) {
		t.Errorf("expected reason carried from pending, got:\n%s", text)
	}
	if !strings.Contains(text, `"cloudContextName": "mock-cluster"`) {
		t.Errorf("expected cloud context name from pending, got:\n%s", text)
	}
	if !strings.Contains(text, `"workingHours": "09:00-18:00"`) {
		t.Errorf("expected working-hours snapshot, got:\n%s", text)
	}
	if !strings.Contains(text, `"name": "terminal-stdin"`) || !strings.Contains(text, `"name": "ai"`) {
		t.Errorf("expected per-marker breakdown carried through, got:\n%s", text)
	}
}

func testActivityRecordStopHostManualWithoutPending(t *testing.T) {
	// The path the desktop's Stop button takes when the user stops before the
	// idle policy has armed any grace — no pending file, yet the row must still
	// record the source and reason.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{
		"activity", "record-stop",
		"--tenant", "team",
		"--environment", "dev",
		"--source", "host-manual",
		"--reason", "Manual stop via desktop",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	body, err := os.ReadFile(filepath.Join(setup.Home, ".erun", "team", "dev", "stop-history.json"))
	if err != nil {
		t.Fatalf("read stop-history.json: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, `"source": "host-manual"`) {
		t.Errorf("expected source=host-manual, got:\n%s", text)
	}
	if !strings.Contains(text, `"reason": "Manual stop via desktop"`) {
		t.Errorf("expected explicit reason carried through, got:\n%s", text)
	}
	// These fields are JSON-omitempty, so without a pending file they must be
	// absent entirely, not empty.
	if strings.Contains(text, `"armedAt"`) {
		t.Errorf("expected no armedAt without pending, got:\n%s", text)
	}
	if strings.Contains(text, `"policy"`) {
		t.Errorf("expected no policy snapshot without pending, got:\n%s", text)
	}
}

func testActivityStopReadyArmsGraceForManagedCloudEnv(t *testing.T) {
	// First eligible stop-ready call on a cloud-managed idle env arms the grace
	// and exits non-zero, so the in-pod monitor does not stop the instance yet.
	// With no activity recorded every marker is idle, so the env is stop-eligible
	// regardless of wall clock (outside working hours short-circuits to eligible;
	// inside, all markers idle does the same).
	setup := env.New(t)
	seedManagedCloudTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"activity", "stop-ready", "--tenant", "team", "--environment", "dev", "--cloud-context", "mock-cluster"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit on Arm, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "activity/stop_ready_arms_grace_for_managed_cloud_env", normalize.Apply(result.Combined))
	body, err := os.ReadFile(filepath.Join(setup.Home, ".erun", "team", "dev", "stop-pending.json"))
	if err != nil {
		t.Fatalf("expected stop-pending.json armed on disk: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		`"graceSeconds": 300`,
		`"cloudContextName": "mock-cluster"`,
		`"reasonSummary": "idle: ssh, api, mcp, cli, codex, process, lease"`,
		`"name": "ssh"`,
		`"workingHours": "08:00-20:00"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("expected pending file to contain %s, got:\n%s", want, text)
		}
	}
}

func testActivityStopReadyWaitsWhileGraceWindowOpen(t *testing.T) {
	// Second eligible call inside the grace window takes the Wait branch: the
	// pending file stays byte-identical. The remaining-seconds value depends on
	// real wall time between the two subprocesses, so a golden cannot lock the
	// stream; the branch is asserted structurally, per the intrinsically-variable-line
	// exception in AGENTS.md.
	setup := env.New(t)
	seedManagedCloudTenantEnv(t, setup, "team", "dev")
	arm := erun.Run(t, []string{"activity", "stop-ready", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if arm.ExitCode == 0 {
		t.Fatalf("expected non-zero exit on Arm, got 0:\n%s", arm.Combined)
	}
	pendingPath := filepath.Join(setup.Home, ".erun", "team", "dev", "stop-pending.json")
	armed, err := os.ReadFile(pendingPath)
	if err != nil {
		t.Fatalf("expected stop-pending.json armed on disk: %v", err)
	}
	result := erun.Run(t, []string{"activity", "stop-ready", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit on Wait, got 0:\n%s", result.Combined)
	}
	waitLine := regexp.MustCompile(`auto-stop pending: \d+ seconds remaining in grace`)
	if !waitLine.MatchString(result.Combined) {
		t.Errorf("expected Wait-branch message, got:\n%s", result.Combined)
	}
	after, err := os.ReadFile(pendingPath)
	if err != nil {
		t.Fatalf("expected stop-pending.json to survive Wait: %v", err)
	}
	if string(after) != string(armed) {
		t.Errorf("expected pending file unchanged through Wait:\narmed:\n%s\nafter:\n%s", armed, after)
	}
}

func testActivityStopReadyFiresAfterGraceElapsed(t *testing.T) {
	// Fire branch: a seeded Since long in the past keeps elapsed >> grace
	// deterministic for decades. Exit 0 is the only exit-0 outcome — the
	// monitor's cue to call ec2:StopInstances — and stop-pending.json is
	// cleared before reporting for crash safety.
	setup := env.New(t)
	seedManagedCloudTenantEnv(t, setup, "team", "dev")
	pendingPath := seedStopPending(t, setup.Home, "team", "dev", `{
  "since": "2026-01-01T00:00:00Z",
  "graceSeconds": 600,
  "cloudContextName": "mock-cluster",
  "reasonSummary": "idle: terminal-stdin, ai",
  "markers": [
    {"name": "terminal-stdin", "idle": true, "reason": "no input"}
  ],
  "policy": {
    "timeout": 600000000000,
    "workingHours": "09:00-18:00",
    "idleTrafficBytes": 0
  }
}
`)
	result := erun.Run(t, []string{"activity", "stop-ready", "--json", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("expected exit 0 on Fire, got %d:\n%s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "activity/stop_ready_fires_after_grace_elapsed", normalize.Apply(result.Combined))
	// Normalization masks every timestamp to <TS>, so the seeded Since
	// flowing through to pendingSince is asserted on the raw stream.
	if !strings.Contains(result.Stdout, `"pendingSince":"2026-01-01T00:00:00Z"`) {
		t.Errorf("expected seeded since to surface as pendingSince, got:\n%s", result.Stdout)
	}
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Errorf("expected stop-pending.json cleared on Fire, stat err: %v", err)
	}
}

func testActivityStopReadySkipClearsStalePending(t *testing.T) {
	// Skip branch: when eligibility lapses (here the env is no longer
	// cloud-managed), a leftover stop-pending.json must be deleted so a stale
	// stop cannot fire later.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	pendingPath := seedStopPending(t, setup.Home, "team", "dev", `{"since": "2026-01-01T00:00:00Z", "graceSeconds": 600}
`)
	result := erun.Run(t, []string{"activity", "stop-ready", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit on Skip, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "activity/stop_ready_skip_clears_stale_pending", normalize.Apply(result.Combined))
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Errorf("expected stale stop-pending.json cleared on Skip, stat err: %v", err)
	}
}

func testActivityCancelStopPendingRemovesArmedGrace(t *testing.T) {
	// cancel-stop-pending is the desktop Cancel button's dismissal path: it
	// removes the pending file and exits 0, with no status resolution.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	pendingPath := seedStopPending(t, setup.Home, "team", "dev", `{"since": "2026-01-01T00:00:00Z", "graceSeconds": 600}
`)
	result := erun.Run(t, []string{"activity", "cancel-stop-pending", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "activity/cancel_stop_pending_removes_armed_grace", normalize.Apply(result.Combined))
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Errorf("expected stop-pending.json removed, stat err: %v", err)
	}
}

func testActivityStopReadyRequiresTarget(t *testing.T) {
	// The in-pod monitor must get a clear error, not a stack of resolver noise,
	// when it invokes stop-ready without the target flags.
	setup := env.New(t)
	result := erun.Run(t, []string{"activity", "stop-ready"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit without target flags, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "activity/stop_ready_requires_target", normalize.Apply(result.Combined))
}

func testActivityStopReadyErrorsOnCorruptPending(t *testing.T) {
	// A corrupt stop-pending.json must fail loudly, not be silently treated as
	// "no grace armed" (which would re-arm and stretch the stop window forever).
	// Status readers swallow the same load error by design, so the failure
	// surfaces only from the decision path.
	setup := env.New(t)
	seedManagedCloudTenantEnv(t, setup, "team", "dev")
	seedStopPending(t, setup.Home, "team", "dev", "{not-json\n")
	result := erun.Run(t, []string{"activity", "stop-ready", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for corrupt pending file, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "activity/stop_ready_errors_on_corrupt_pending", normalize.Apply(result.Combined))
	// The <TMP> rule swallows the whole path including the file name,
	// so the "error names the offending file" contract is asserted on
	// the raw stream.
	if !strings.Contains(result.Combined, "stop-pending.json:") {
		t.Errorf("expected error to name stop-pending.json, got:\n%s", result.Combined)
	}
}

func testActivityRecordStopErrorsOnCorruptHistory(t *testing.T) {
	// A corrupt stop-history.json must fail the append loudly instead of
	// silently overwriting the audit trail.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	historyDir := filepath.Join(setup.Home, ".erun", "team", "dev")
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", historyDir, err)
	}
	if err := os.WriteFile(filepath.Join(historyDir, "stop-history.json"), []byte("{not-json\n"), 0o600); err != nil {
		t.Fatalf("write corrupt history: %v", err)
	}
	result := erun.Run(t, []string{"activity", "record-stop", "--tenant", "team", "--environment", "dev", "--source", "host-manual", "--reason", "Manual stop"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for corrupt history file, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "activity/record_stop_errors_on_corrupt_history", normalize.Apply(result.Combined))
	// The <TMP> rule swallows the whole path including the file name,
	// so the "error names the offending file" contract is asserted on
	// the raw stream.
	if !strings.Contains(result.Combined, "stop-history.json:") {
		t.Errorf("expected error to name stop-history.json, got:\n%s", result.Combined)
	}
}

func testActivityCancelStopPendingRequiresTarget(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"activity", "cancel-stop-pending"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit without target flags, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "activity/cancel_stop_pending_requires_target", normalize.Apply(result.Combined))
}

func testActivityRecordStopFallsBackToOnDiskPending(t *testing.T) {
	// A manual stop during an armed grace: with no --state-stdin, record-stop
	// recovers the grace/policy/markers from the on-disk pending file, then
	// clears it. A second record-stop locks newest-first ordering: the new row
	// is prepended.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	pendingPath := seedStopPending(t, setup.Home, "team", "dev", `{
  "since": "2026-05-31T12:20:00Z",
  "graceSeconds": 600,
  "cloudContextName": "mock-cluster",
  "reasonSummary": "idle: ssh",
  "markers": [
    {"name": "ssh", "idle": true, "reason": "last activity exceeded timeout", "lastActivity": "2026-05-31T12:10:00Z"},
    {"name": "cli", "idle": true, "reason": "no activity recorded"}
  ],
  "policy": {"timeout": 600000000000, "workingHours": "09:00-18:00", "idleTrafficBytes": 0}
}
`)
	result := erun.Run(t, []string{"activity", "record-stop", "--tenant", "team", "--environment", "dev", "--source", "host-manual"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	historyPath := filepath.Join(setup.Home, ".erun", "team", "dev", "stop-history.json")
	body, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("read stop-history.json: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		`"source": "host-manual"`,
		`"reason": "idle: ssh"`,
		`"armedAt": "2026-05-31T12:20:00Z"`,
		`"graceSeconds": 600`,
		`"secondsIdleFor": 600`,
		`"cloudContextName": "mock-cluster"`,
		`"workingHours": "09:00-18:00"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("expected history to contain %s, got:\n%s", want, text)
		}
	}
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Errorf("expected stop-pending.json cleared after record-stop, stat err: %v", err)
	}
	recordSecondStopNewestFirst(t, setup, historyPath)
}

// recordSecondStopNewestFirst records a second manual stop and requires it to
// be prepended ahead of the stop already in the history.
func recordSecondStopNewestFirst(t *testing.T, setup env.Setup, historyPath string) {
	t.Helper()
	second := erun.Run(t, []string{"activity", "record-stop", "--tenant", "team", "--environment", "dev", "--source", "host-manual", "--reason", "Manual stop via desktop"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if second.ExitCode != 0 {
		t.Fatalf("exit %d: %s", second.ExitCode, second.Combined)
	}
	body, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("read stop-history.json after second stop: %v", err)
	}
	var rows []struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("parse stop-history.json: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 history rows, got %d:\n%s", len(rows), body)
	}
	if rows[0].Reason != "Manual stop via desktop" || rows[1].Reason != "idle: ssh" {
		t.Errorf("expected newest-first ordering, got %q then %q", rows[0].Reason, rows[1].Reason)
	}
}

func testActivityRecordStopCapsHistoryAtTen(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	for i := 1; i <= 11; i++ {
		result := erun.Run(t, []string{"activity", "record-stop", "--tenant", "team", "--environment", "dev", "--source", "pod-monitor", "--reason", fmt.Sprintf("stop %02d", i)}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("record-stop %d: exit %d: %s", i, result.ExitCode, result.Combined)
		}
	}
	body, err := os.ReadFile(filepath.Join(setup.Home, ".erun", "team", "dev", "stop-history.json"))
	if err != nil {
		t.Fatalf("read stop-history.json: %v", err)
	}
	var rows []struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("parse stop-history.json: %v", err)
	}
	if len(rows) != 10 {
		t.Fatalf("expected history capped at 10 rows, got %d", len(rows))
	}
	if rows[0].Reason != "stop 11" || rows[9].Reason != "stop 02" {
		t.Errorf("expected newest-first window stop 11..stop 02, got %q .. %q", rows[0].Reason, rows[9].Reason)
	}
}
