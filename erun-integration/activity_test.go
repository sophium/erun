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

// activity is a hidden command group used by the runtime entrypoint to record
// SSH/MCP/CLI/Codex traffic. Its subcommands are exercised here so the activity
// package is covered without spinning up a real proxy.

// seedManagedCloudTenantEnv writes the same tree as fixture.SeedTenantEnv and
// flips managedcloud: true on the env config, so the shared idle resolver
// treats the env as cloud-managed without needing a cloud-context store. This
// unlocks the Arm/Wait/Fire arms of eruncommon.MaybeArmOrFireIdleStop, which
// are gated on Status.ManagedCloud && Status.StopEligible.
func seedManagedCloudTenantEnv(t *testing.T, setup env.Setup, tenant, environment string) {
	t.Helper()
	fixture.SeedTenantEnv(t, setup, tenant, environment)
	cfgPath := filepath.Join(setup.ConfigHome, "erun", tenant, environment, "config.yaml")
	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read env config %s: %v", cfgPath, err)
	}
	if err := os.WriteFile(cfgPath, append(body, []byte("managedcloud: true\n")...), 0o644); err != nil {
		t.Fatalf("write env config %s: %v", cfgPath, err)
	}
}

// seedStopPending writes <home>/.erun/<tenant>/<env>/stop-pending.json
// directly so a scenario can enter the grace state machine mid-flight with an
// explicit, deterministic Since timestamp instead of sleeping through a real
// grace window. Returns the file path for on-disk asserts.
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

// seedActivitySnapshot writes the per-kind activity JSON under the XDG cache
// tree (the same file `activity touch` maintains) so scenarios can seed stale
// lastActivity/lastSeen timestamps that stay deterministically past the idle
// timeout for decades.
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

	t.Run("stop_ready_arms_grace_for_managed_cloud_env", func(t *testing.T) {
		// First eligible stop-ready call on a cloud-managed idle env takes
		// MaybeArmOrFireIdleStop's Arm branch: SaveEnvironmentStopPending
		// writes stop-pending.json with the resolved grace (= the idle
		// timeout, 300s by default), the per-marker snapshot, and the
		// --cloud-context value, then the command exits non-zero so the
		// in-pod monitor does not stop the instance yet. With no activity
		// recorded every kind marker is idle, so the env is stop-eligible
		// regardless of wall clock (outside working hours short-circuits to
		// eligible; inside, all markers being idle does the same).
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
			`"reasonSummary": "idle: ssh, api, mcp, cli, codex"`,
			`"name": "ssh"`,
			`"workingHours": "08:00-20:00"`,
		} {
			if !strings.Contains(text, want) {
				t.Errorf("expected pending file to contain %s, got:\n%s", want, text)
			}
		}
	})

	t.Run("stop_ready_waits_while_grace_window_open", func(t *testing.T) {
		// Second eligible call inside the grace window takes the Wait
		// branch: stop-pending.json stays byte-identical and the command
		// exits non-zero reporting the seconds remaining. The
		// remaining-seconds value depends on real wall time elapsed between
		// the two subprocess invocations, so the stream cannot be locked by
		// a golden; the branch is asserted structurally (message shape +
		// unchanged pending file), per the intrinsically-variable-line
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
	})

	t.Run("stop_ready_fires_after_grace_elapsed", func(t *testing.T) {
		// Fire branch: a seeded grace window whose Since is long past keeps
		// elapsed >> grace deterministic for decades. stop-ready must exit 0
		// (the only exit-0 outcome — the monitor's cue to call
		// ec2:StopInstances), emit the --json payload with action=fire and
		// the just-cleared pending entry under pendingState, and remove
		// stop-pending.json before reporting (crash safety). The payload's
		// graceSeconds=600 comes from overlayStopPending reading the same
		// seeded file with its remaining-seconds clamped to 0.
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
	})

	t.Run("stop_ready_skip_clears_stale_pending", func(t *testing.T) {
		// Skip branch: when eligibility lapses (here: the env is not
		// cloud-managed), a leftover stop-pending.json from an earlier grace
		// window must be deleted so a stale stop cannot fire later. The
		// command still exits non-zero with the blocked reason.
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
	})

	t.Run("cancel_stop_pending_removes_armed_grace", func(t *testing.T) {
		// `activity cancel-stop-pending` is the desktop Cancel button's
		// dismissal path: it must remove stop-pending.json and exit 0. The
		// command takes no status resolution, so only the seeded pending
		// file matters.
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
	})

	t.Run("stop_ready_requires_target", func(t *testing.T) {
		// resolveActivityStatus validation arm via stop-ready: the in-pod
		// monitor must get a clear error (not a stack of resolver noise)
		// when it forgets the target flags.
		setup := env.New(t)
		result := erun.Run(t, []string{"activity", "stop-ready"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without target flags, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "activity/stop_ready_requires_target", normalize.Apply(result.Combined))
	})

	t.Run("stop_ready_errors_on_corrupt_pending", func(t *testing.T) {
		// LoadEnvironmentStopPending's malformed-contents arm: a corrupt
		// stop-pending.json must fail loudly with the file path (not be
		// silently treated as "no grace armed", which would re-arm and
		// stretch the stop window forever). overlayStopPending swallows the
		// same load error by design — status readers stay usable — so the
		// failure surfaces from the decision function.
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
	})

	t.Run("record_stop_errors_on_corrupt_history", func(t *testing.T) {
		// readStopHistoryFile's malformed-contents arm: a corrupt
		// stop-history.json must fail the append loudly with the file path
		// instead of silently overwriting the audit trail.
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
	})

	t.Run("cancel_stop_pending_requires_target", func(t *testing.T) {
		// Validation arm of cancel-stop-pending: without --tenant and
		// --environment the command must fail before touching any state.
		setup := env.New(t)
		result := erun.Run(t, []string{"activity", "cancel-stop-pending"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without target flags, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "activity/cancel_stop_pending_requires_target", normalize.Apply(result.Combined))
	})

	t.Run("record_stop_falls_back_to_on_disk_pending", func(t *testing.T) {
		// Layer-2 recovery in loadPendingForRecord: no --state-stdin, but
		// stop-pending.json still exists (a manual stop during an armed
		// grace). The history row must fold grace/armedAt/policy/markers
		// from the on-disk pending entry, compute secondsIdleFor off the
		// marker's lastActivity relative to the armed Since, and clear the
		// pending file once the row is persisted. A second record-stop then
		// locks readStopHistoryFile's parse-existing branch: the new row is
		// prepended newest-first.
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
		second := erun.Run(t, []string{"activity", "record-stop", "--tenant", "team", "--environment", "dev", "--source", "host-manual", "--reason", "Manual stop via desktop"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if second.ExitCode != 0 {
			t.Fatalf("exit %d: %s", second.ExitCode, second.Combined)
		}
		body, err = os.ReadFile(historyPath)
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
	})

	t.Run("record_stop_caps_history_at_ten", func(t *testing.T) {
		// AppendStopHistoryEntry truncates the audit array to
		// StopHistoryCap (10) newest-first: the 11th stop must push the
		// first one off the tail.
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
	})

	t.Run("touch_rejects_unknown_kind", func(t *testing.T) {
		// RecordEnvironmentActivity's kind validation: an unsupported kind
		// must fail loudly instead of writing a stray snapshot file.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"activity", "touch", "--tenant", "team", "--environment", "dev", "--kind", "bogus"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for unknown kind, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "activity/touch_rejects_unknown_kind", normalize.Apply(result.Combined))
	})

	t.Run("touch_evicts_oldest_client_beyond_cap", func(t *testing.T) {
		// environmentActivityClientCap is 8: the 9th distinct client
		// address must evict the LRU entry. The first-touched address is
		// deterministically the oldest because the touches run as
		// sequential subprocesses, each stamping LastActivity with its own
		// wall-clock now.
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
	})

	t.Run("status_distinguishes_stale_and_codex_open_markers", func(t *testing.T) {
		// activityIdleMarker branches: a marker whose lastActivity exceeded
		// the timeout reports "last activity exceeded timeout", while the
		// codex marker with a fresh LastSeen heartbeat (touch --seen) but a
		// stale lastActivity reports "codex is open but idle". Stale
		// timestamps are seeded directly (2026-01-01 stays past the 300s
		// timeout for decades). Whole-stream golden is impossible here: the
		// working-hours line depends on wall clock.
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
	})

	t.Run("ssh_proxy_requires_tenant_and_environment", func(t *testing.T) {
		// runActivitySSHProxy validation arm: target identity is mandatory
		// before any socket is opened.
		setup := env.New(t)
		result := erun.Run(t, []string{"activity", "ssh-proxy", "--listen", "127.0.0.1:0", "--target", "127.0.0.1:1"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without tenant/environment, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "activity/ssh_proxy_requires_tenant_and_environment", normalize.Apply(result.Combined))
	})

	t.Run("ssh_proxy_requires_listen_and_target", func(t *testing.T) {
		// runActivitySSHProxy validation arm: both proxy addresses are
		// required before any socket is opened.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"activity", "ssh-proxy", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without addresses, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "activity/ssh_proxy_requires_listen_and_target", normalize.Apply(result.Combined))
	})

	t.Run("ssh_proxy_rejects_negative_idle_traffic_threshold", func(t *testing.T) {
		// runActivitySSHProxy validation arm: a negative byte threshold is
		// a misconfiguration, not "always idle".
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"activity", "ssh-proxy", "--tenant", "team", "--environment", "dev", "--listen", "127.0.0.1:0", "--target", "127.0.0.1:1", "--idle-traffic-bytes=-1"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for negative threshold, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "activity/ssh_proxy_rejects_negative_idle_traffic_threshold", normalize.Apply(result.Combined))
	})

	t.Run("ssh_proxy_rejects_unlistenable_address", func(t *testing.T) {
		// runActivitySSHProxy's net.Listen error arm: a listen address with
		// no port fails deterministically (no DNS, no port collision) so
		// the listener-setup error path is locked without starting the
		// accept loop.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"activity", "ssh-proxy", "--tenant", "team", "--environment", "dev", "--listen", "127.0.0.1", "--target", "127.0.0.1:1"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for unlistenable address, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "activity/ssh_proxy_rejects_unlistenable_address", normalize.Apply(result.Combined))
	})
}
