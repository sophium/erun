package integration

import (
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

// activity is a hidden command group used by the runtime entrypoint to record
// SSH/MCP/CLI/Codex traffic. The dry-run-friendly subcommands `touch`,
// `status`, and `stop-ready` are exercised here so the activity package is
// covered without spinning up a real proxy.

func TestActivity(t *testing.T) {
	t.Run("touch_records_cli_activity", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"activity", "touch", "--tenant", "team", "--environment", "dev", "--kind", "cli"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "activity/touch_records_cli_activity", normalize.Apply(result.Combined))
	})

	t.Run("status_with_seeded_env", func(t *testing.T) {
		// Exercises erun-cli/cmd/activity.go writeActivityStatus + the
		// shared idle resolver. The working-hours line varies by wall
		// clock; assert the stable lines exactly and the variable line
		// structurally so the test is time-of-day-agnostic.
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
		// working-hours: idle (outside working hours 08:00-20:00) OR
		// active (inside working hours 08:00-20:00) — both shapes valid.
		workingHours := regexp.MustCompile(`(?m)^\s*working-hours: (idle|active) \((inside|outside) working hours \d{2}:\d{2}-\d{2}:\d{2}\)\s*$`)
		if !workingHours.MatchString(result.Stdout) {
			t.Errorf("expected working-hours marker line, got:\n%s", result.Stdout)
		}
	})

	t.Run("status_json_output", func(t *testing.T) {
		// Exercises activity.go --json branch: structured status output
		// via json.NewEncoder bypasses writeActivityStatus's text format.
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
	})

	t.Run("stop_ready_blocks_when_active", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		// Touch CLI first to make the env not idle, then stop-ready should
		// exit non-zero with a blocked reason.
		erun.Run(t, []string{"activity", "touch", "--tenant", "team", "--environment", "dev", "--kind", "cli"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		result := erun.Run(t, []string{"activity", "stop-ready", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "activity/stop_ready_blocks_when_active", normalize.Apply(result.Combined))
	})

	t.Run("status_json_includes_per_client_breakdown", func(t *testing.T) {
		// Exercises the SSH-proxy contract that ships per-IP byte
		// deltas via EnvironmentActivityParams.ClientUpdates. The
		// desktop tooltip and external `activity status --json`
		// consumers both read the resulting `clients` slice off the
		// marker, so the JSON contract is locked here.
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
	})

	t.Run("stop_ready_json_emits_structured_decision", func(t *testing.T) {
		// Exercises the --json flag wired for the runtime entrypoint's
		// idle-monitor heartbeat log. JSON must land on stdout regardless of
		// the stop-eligible exit code so the bash loop can record a tick
		// even when the env stays active.
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
	})

	t.Run("record_stop_folds_state_stdin_into_history", func(t *testing.T) {
		// The in-pod monitor pipes the stop-ready --json blob into
		// `record-stop --state-stdin --source pod-monitor` because the
		// Fire branch of stop-ready has already cleared
		// stop-pending.json by the time record-stop runs. This
		// scenario locks that contract end-to-end: pipe a synthetic
		// stop-ready JSON in, then read the resulting
		// stop-history.json off the per-env runtime state dir and
		// assert source/grace/policy/markers all made the round trip
		// — which is what the History tab needs to answer "what
		// triggered it?"
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
		// Source must be the value supplied via the flag — that is
		// the field the History tab uses to render the row badge.
		if !strings.Contains(text, `"source": "pod-monitor"`) {
			t.Errorf("expected source=pod-monitor in history, got:\n%s", text)
		}
		// Grace + reason + cloud context must all fold from the
		// piped pending payload — the on-disk pending file is
		// already gone by the time the monitor calls record-stop.
		if !strings.Contains(text, `"graceSeconds": 600`) {
			t.Errorf("expected graceSeconds=600 carried through stdin, got:\n%s", text)
		}
		if !strings.Contains(text, `"reason": "idle: terminal-stdin, ai"`) {
			t.Errorf("expected reason carried from pending, got:\n%s", text)
		}
		if !strings.Contains(text, `"cloudContextName": "mock-cluster"`) {
			t.Errorf("expected cloud context name from pending, got:\n%s", text)
		}
		// Policy snapshot + markers must survive the round trip.
		if !strings.Contains(text, `"workingHours": "09:00-18:00"`) {
			t.Errorf("expected working-hours snapshot, got:\n%s", text)
		}
		if !strings.Contains(text, `"name": "terminal-stdin"`) || !strings.Contains(text, `"name": "ai"`) {
			t.Errorf("expected per-marker breakdown carried through, got:\n%s", text)
		}
	})

	t.Run("record_stop_host_manual_without_pending", func(t *testing.T) {
		// Manual desktop stop without a prior armed grace. No stdin,
		// no pending file on disk — the resulting row should still
		// land with source=host-manual and the explicit reason
		// flagged through. This is the bare path the desktop's Stop
		// button takes when the user has not let the idle policy
		// build any grace.
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
		// Without a pending file the row carries no grace, no
		// armedAt, and no policy. Those fields are JSON-omitempty so
		// they should not appear at all.
		if strings.Contains(text, `"armedAt"`) {
			t.Errorf("expected no armedAt without pending, got:\n%s", text)
		}
		if strings.Contains(text, `"policy"`) {
			t.Errorf("expected no policy snapshot without pending, got:\n%s", text)
		}
	})
}
