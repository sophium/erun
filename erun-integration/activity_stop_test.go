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
	// The idle-monitor loop needs the JSON regardless of the exit code.
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
	// Fire already cleared stop-pending.json, so the state round-trips via stdin.
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
	// omitempty: absent, not empty.
	if strings.Contains(text, `"armedAt"`) {
		t.Errorf("expected no armedAt without pending, got:\n%s", text)
	}
	if strings.Contains(text, `"policy"`) {
		t.Errorf("expected no policy snapshot without pending, got:\n%s", text)
	}
}

func testActivityStopReadyArmsGraceForManagedCloudEnv(t *testing.T) {
	// With no activity every marker is idle, so this is eligible at any hour.
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
	// The remaining seconds depend on wall time, so this is asserted
	// structurally (AGENTS.md's intrinsically-variable-line exception).
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
	// A Since far in the past keeps elapsed > grace deterministic.
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
	// Timestamps normalize to <TS>, so check the raw stream.
	if !strings.Contains(result.Stdout, `"pendingSince":"2026-01-01T00:00:00Z"`) {
		t.Errorf("expected seeded since to surface as pendingSince, got:\n%s", result.Stdout)
	}
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Errorf("expected stop-pending.json cleared on Fire, stat err: %v", err)
	}
}

func testActivityStopReadySkipClearsStalePending(t *testing.T) {
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
	setup := env.New(t)
	result := erun.Run(t, []string{"activity", "stop-ready"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit without target flags, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "activity/stop_ready_requires_target", normalize.Apply(result.Combined))
}

func testActivityStopReadyErrorsOnCorruptPending(t *testing.T) {
	// Treating a corrupt file as "no grace armed" would re-arm forever.
	setup := env.New(t)
	seedManagedCloudTenantEnv(t, setup, "team", "dev")
	seedStopPending(t, setup.Home, "team", "dev", "{not-json\n")
	result := erun.Run(t, []string{"activity", "stop-ready", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for corrupt pending file, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "activity/stop_ready_errors_on_corrupt_pending", normalize.Apply(result.Combined))
	// <TMP> swallows the file name, so check the raw stream.
	if !strings.Contains(result.Combined, "stop-pending.json:") {
		t.Errorf("expected error to name stop-pending.json, got:\n%s", result.Combined)
	}
}

func testActivityRecordStopErrorsOnCorruptHistory(t *testing.T) {
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
	// <TMP> swallows the file name, so check the raw stream.
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
	// Without --state-stdin, record-stop recovers state from the pending file.
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
