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

// activity is a hidden command group the runtime entrypoint uses to record
// SSH/MCP/CLI/Codex traffic; its subcommands are exercised here.

// The working-hours window decides which arm of the stop-eligibility branch a
// scenario takes, and the resolved policy always has one (the default is
// 08:00-20:00 in the host's zone), so a scenario that leaves it alone exercises
// a different arm depending on the wall clock and the host's TZ. These two
// blocks pin the arm: each is the widest window the parser accepts (start and
// end must differ), inverted, so only the 23:59 UTC minute falls on the other
// side. Landing on the other side there costs nothing — a held lease refuses the
// stop identically on both arms, which is the point these scenarios make — so
// what the pin buys is that each scenario reliably exercises the arm it names.
const (
	insideWorkingHoursIdleBlock  = "idle:\n  workinghours: 00:00-23:59\n  timezone: UTC\n"
	outsideWorkingHoursIdleBlock = "idle:\n  workinghours: 23:59-00:00\n  timezone: UTC\n"
)

// seedManagedCloudTenantEnv marks the seeded env cloud-managed so the idle
// resolver's Arm/Wait/Fire branches become reachable.
func seedManagedCloudTenantEnv(t *testing.T, setup env.Setup, tenant, environment string) {
	t.Helper()
	seedManagedCloudTenantEnvWithIdle(t, setup, tenant, environment, "")
}

// seedManagedCloudTenantEnvWithIdle is seedManagedCloudTenantEnv with an
// explicit idle block appended, so a scenario whose outcome depends on the
// working-hours window can pin that window instead of inheriting the default
// 08:00-20:00 and reading differently depending on the wall clock.
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

// seedStopPending lets a scenario enter the grace state machine mid-flight with
// a deterministic Since timestamp instead of sleeping through a real grace window.
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

// seedActivitySnapshot lets scenarios seed stale activity timestamps that stay
// deterministically past the idle timeout for decades.
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

// writeSampleProcess lays down one /proc/<pid>/stat entry so the sampler's
// verdict is driven by the fixture rather than by whatever the host happens to
// be running.
func writeSampleProcess(t *testing.T, root string, pid int, comm string, cpuTicks, startTime int64) {
	t.Helper()
	writeSampleProcessWithTTY(t, root, pid, comm, cpuTicks, startTime, 0)
}

// writeSampleProcessWithTTY is writeSampleProcess with an explicit tty_nr
// (column 7) and no parent, for a lone process with - or without - an
// allocated pseudo-terminal.
func writeSampleProcessWithTTY(t *testing.T, root string, pid int, comm string, cpuTicks, startTime, ttyNr int64) {
	t.Helper()
	writeSampleProcessFull(t, root, pid, 0, comm, cpuTicks, startTime, ttyNr)
}

// writeSampleProcessFull is writeSampleProcessWithTTY with an explicit ppid
// (column 4), so a scenario can model the real shape of an SSH session: the
// per-session "sshd: user@ptsN" process stays tty-less itself while the
// command it forks becomes the pty's session leader.
func writeSampleProcessFull(t *testing.T, root string, pid int, ppid int64, comm string, cpuTicks, startTime, ttyNr int64) {
	t.Helper()
	dir := filepath.Join(root, fmt.Sprintf("%d", pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	fields := []string{fmt.Sprintf("%d (%s) S", pid, comm)}
	// Column 4 (ppid).
	fields = append(fields, fmt.Sprintf("%d", ppid))
	// Columns 5..6 (pgrp, session) are unused padding.
	fields = append(fields, "0", "0")
	// Column 7 (tty_nr): 0 means no controlling terminal.
	fields = append(fields, fmt.Sprintf("%d", ttyNr))
	// Columns 8..13 (tpgid through cmajflt) are unused padding.
	for i := 0; i < 6; i++ {
		fields = append(fields, "0")
	}
	fields = append(fields, fmt.Sprintf("%d", cpuTicks), "0")
	for i := 0; i < 6; i++ {
		fields = append(fields, "0")
	}
	fields = append(fields, fmt.Sprintf("%d", startTime), "0", "0")
	body := strings.Join(fields, " ") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s/stat: %v", dir, err)
	}
}

// writeCgroupFixture lays down a cgroup v2 tree so the usage reader's verdict
// comes from the fixture. The real /sys/fs/cgroup says something different on
// every machine, and on a cgroup v1 host it says nothing at all.
func writeCgroupFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s/%s: %v", root, name, err)
		}
	}
}

// cgroupV2Fixture is the shape measured in a live runtime container: a 12-core
// quota, a 23552Mi limit, and a high-water mark at roughly half of it.
func cgroupV2Fixture(usageUsec, periods, throttled, peakBytes, oomKills int64) map[string]string {
	return map[string]string{
		"cpu.max":        "1200000 100000\n",
		"cpu.stat":       fmt.Sprintf("usage_usec %d\nnr_periods %d\nnr_throttled %d\nthrottled_usec 0\n", usageUsec, periods, throttled),
		"memory.max":     "24696061952\n",
		"memory.current": "4773695488\n",
		"memory.peak":    fmt.Sprintf("%d\n", peakBytes),
		"memory.events":  fmt.Sprintf("low 0\nhigh 0\nmax 0\noom 0\noom_kill %d\n", oomKills),
	}
}

// readUsageHistory reads the retained usage store. It is a side effect outside
// the captured streams, so a golden cannot assert it.
func readUsageHistory(t *testing.T, cacheHome, tenant, environment string) map[string]any {
	t.Helper()
	path := filepath.Join(cacheHome, "erun", "activity", tenant, environment, "usage-history.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var history map[string]any
	if err := json.Unmarshal(body, &history); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return history
}

func TestActivity(t *testing.T) {
	t.Parallel()
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
		// The working-hours line varies by wall clock; assert the stable lines
		// exactly and the variable line structurally so the test is time-of-day-agnostic.
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
	})

	t.Run("status_json_output", func(t *testing.T) {
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
		erun.Run(t, []string{"activity", "touch", "--tenant", "team", "--environment", "dev", "--kind", "cli"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		result := erun.Run(t, []string{"activity", "stop-ready", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "activity/stop_ready_blocks_when_active", normalize.Apply(result.Combined))
	})

	t.Run("status_json_includes_per_client_breakdown", func(t *testing.T) {
		// The desktop tooltip and external `activity status --json` consumers both
		// read the per-client `clients` breakdown, so this JSON contract is locked here.
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
	})

	t.Run("record_stop_folds_state_stdin_into_history", func(t *testing.T) {
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
	})

	t.Run("record_stop_host_manual_without_pending", func(t *testing.T) {
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
	})

	t.Run("stop_ready_arms_grace_for_managed_cloud_env", func(t *testing.T) {
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
	})

	t.Run("stop_ready_waits_while_grace_window_open", func(t *testing.T) {
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
	})

	t.Run("stop_ready_fires_after_grace_elapsed", func(t *testing.T) {
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
	})

	t.Run("stop_ready_skip_clears_stale_pending", func(t *testing.T) {
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
	})

	t.Run("cancel_stop_pending_removes_armed_grace", func(t *testing.T) {
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
	})

	t.Run("stop_ready_requires_target", func(t *testing.T) {
		// The in-pod monitor must get a clear error, not a stack of resolver noise,
		// when it invokes stop-ready without the target flags.
		setup := env.New(t)
		result := erun.Run(t, []string{"activity", "stop-ready"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without target flags, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "activity/stop_ready_requires_target", normalize.Apply(result.Combined))
	})

	t.Run("stop_ready_errors_on_corrupt_pending", func(t *testing.T) {
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
	})

	t.Run("record_stop_errors_on_corrupt_history", func(t *testing.T) {
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
	})

	t.Run("cancel_stop_pending_requires_target", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"activity", "cancel-stop-pending"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without target flags, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "activity/cancel_stop_pending_requires_target", normalize.Apply(result.Combined))
	})

	t.Run("record_stop_falls_back_to_on_disk_pending", func(t *testing.T) {
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
		// An unsupported kind must fail loudly instead of writing a stray snapshot file.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"activity", "touch", "--tenant", "team", "--environment", "dev", "--kind", "bogus"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for unknown kind, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "activity/touch_rejects_unknown_kind", normalize.Apply(result.Combined))
	})

	t.Run("touch_evicts_oldest_client_beyond_cap", func(t *testing.T) {
		// The first-touched address is deterministically the oldest (evicted at the
		// 8-client cap) because the touches run as sequential subprocesses, each
		// stamping LastActivity with its own wall-clock now.
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
		// A stale marker reports "last activity exceeded timeout", while codex with
		// a fresh --seen heartbeat but stale activity reports "codex is open but
		// idle". Timestamps are seeded in the past so they stay idle for decades; no
		// whole-stream golden is possible because the working-hours line depends on
		// wall clock.
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

	t.Run("lease_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"activity", "lease", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "activity/lease_help", normalize.Apply(result.Combined))
	})

	t.Run("lease_take_list_and_release", func(t *testing.T) {
		// The lease lifecycle a wrapper drives: take before the long job, list to
		// see what is holding the environment, release when it finishes. The
		// remaining-seconds figure is wall-clock dependent, so normalization
		// collapses it; the ttl itself is asserted from the JSON below.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		take := erun.Run(t, []string{"activity", "lease", "take", "--tenant", "team", "--environment", "dev", "--name", "gradle-build", "--ttl", "10m"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if take.ExitCode != 0 {
			t.Fatalf("take: exit %d: %s", take.ExitCode, take.Combined)
		}
		list := erun.Run(t, []string{"activity", "lease", "list", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		release := erun.Run(t, []string{"activity", "lease", "release", "--tenant", "team", "--environment", "dev", "--id", "gradle-build"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		empty := erun.Run(t, []string{"activity", "lease", "list", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		golden.Equal(t, "activity/lease_take_list_and_release", normalize.Apply(
			take.Combined+list.Combined+release.Combined+empty.Combined))

		// The persisted lease is a side effect outside the captured streams, and
		// its expiry is what bounds the claim, so it is asserted on disk.
		body, err := os.ReadFile(filepath.Join(setup.CacheHome, "erun", "activity", "team", "dev", "leases", "gradle-build.json"))
		if !os.IsNotExist(err) {
			t.Fatalf("expected the released lease file removed, got err %v body %s", err, body)
		}
	})

	t.Run("lease_take_json_records_the_holder_process", func(t *testing.T) {
		// A wrapper passes its own pid so an abandoned lease is reclaimed rather
		// than waiting out its ttl. The JSON is the wrapper's contract for
		// capturing the id it must release.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"activity", "lease", "take", "--tenant", "team", "--environment", "dev", "--name", "agent run", "--id", "agent-run", "--pid", "4242", "--json"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		var lease struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			PID  int    `json:"pid"`
		}
		if err := json.Unmarshal([]byte(result.Stdout), &lease); err != nil {
			t.Fatalf("parse lease JSON: %v\n%s", err, result.Stdout)
		}
		if lease.ID != "agent-run" || lease.Name != "agent run" || lease.PID != 4242 {
			t.Errorf("unexpected lease %+v", lease)
		}
	})

	t.Run("lease_take_requires_a_name", func(t *testing.T) {
		// A lease with no name would report the environment busy without saying
		// why, which is the whole gap this exists to close.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"activity", "lease", "take", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without a name, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "activity/lease_take_requires_a_name", normalize.Apply(result.Combined))
	})

	t.Run("lease_take_exclusive_dry_run", func(t *testing.T) {
		// erun#1245: the dry-run trace must show the exclusive claim and its
		// resolved scope, since that is the decision the command would make.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{
			"activity", "lease", "take", "--tenant", "team", "--environment", "dev",
			"--name", "job-fix-1245", "--exclusive", "--orchestrator", "petios", "--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "activity/lease_take_exclusive_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("lease_take_exclusive_refuses_a_second_holder_and_names_it", func(t *testing.T) {
		// The collision #1245 exists to close: two agent jobs in the same
		// worktree. The second exclusive take must fail and name the first.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		first := erun.Run(t, []string{
			"activity", "lease", "take", "--tenant", "team", "--environment", "dev",
			"--name", "job-fix-1201", "--id", "job-fix-1201", "--exclusive", "--orchestrator", "petios",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if first.ExitCode != 0 {
			t.Fatalf("first take: exit %d: %s", first.ExitCode, first.Combined)
		}
		second := erun.Run(t, []string{
			"activity", "lease", "take", "--tenant", "team", "--environment", "dev",
			"--name", "job-fix-1245", "--id", "job-fix-1245", "--exclusive", "--orchestrator", "erun",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if second.ExitCode == 0 {
			t.Fatalf("expected the second exclusive take to be refused, got exit 0: %s", second.Combined)
		}
		if !strings.Contains(second.Combined, "job-fix-1201") || !strings.Contains(second.Combined, "petios") {
			t.Fatalf("refusal must name the actual holder (id and orchestrator), got:\n%s", second.Combined)
		}
	})

	t.Run("lease_take_exclusive_allows_a_different_scope_to_coexist", func(t *testing.T) {
		// Two clones of the same repo in one pod is legitimate parallelism,
		// not a collision - exclusivity must be scoped, not environment-wide.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		first := erun.Run(t, []string{
			"activity", "lease", "take", "--tenant", "team", "--environment", "dev",
			"--name", "clone-a-job", "--id", "clone-a-job", "--exclusive", "--scope", "/git/clone-a",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if first.ExitCode != 0 {
			t.Fatalf("first take: exit %d: %s", first.ExitCode, first.Combined)
		}
		second := erun.Run(t, []string{
			"activity", "lease", "take", "--tenant", "team", "--environment", "dev",
			"--name", "clone-b-job", "--id", "clone-b-job", "--exclusive", "--scope", "/git/clone-b",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if second.ExitCode != 0 {
			t.Fatalf("expected a different scope to succeed without conflict, got exit %d: %s", second.ExitCode, second.Combined)
		}
	})

	t.Run("lease_release_exclusive_round_trip", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		take := erun.Run(t, []string{
			"activity", "lease", "take", "--tenant", "team", "--environment", "dev",
			"--name", "job-fix-1245", "--id", "job-fix-1245", "--exclusive",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if take.ExitCode != 0 {
			t.Fatalf("take: exit %d: %s", take.ExitCode, take.Combined)
		}
		release := erun.Run(t, []string{
			"activity", "lease", "release", "--tenant", "team", "--environment", "dev",
			"--id", "job-fix-1245", "--exclusive",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if release.ExitCode != 0 {
			t.Fatalf("release: exit %d: %s", release.ExitCode, release.Combined)
		}
		// The scope is free again: a fresh exclusive take on it succeeds.
		again := erun.Run(t, []string{
			"activity", "lease", "take", "--tenant", "team", "--environment", "dev",
			"--name", "job-fix-1250", "--id", "job-fix-1250", "--exclusive",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if again.ExitCode != 0 {
			t.Fatalf("expected the scope free after release, got exit %d: %s", again.ExitCode, again.Combined)
		}
	})

	t.Run("stop_ready_blocked_by_a_held_lease", func(t *testing.T) {
		// AC6 of the stop work: an otherwise-idle cloud-managed env that holds a
		// lease must not be stopped, and the refusal must name the lease so an
		// operator can see why auto-stop is being deferred. The window is pinned
		// to a UTC day so the scenario stays on the inside-working-hours arm
		// whatever time the suite runs; its outside-hours sibling below pins the
		// other arm.
		setup := env.New(t)
		seedManagedCloudTenantEnvWithIdle(t, setup, "team", "dev", insideWorkingHoursIdleBlock)
		take := erun.Run(t, []string{"activity", "lease", "take", "--tenant", "team", "--environment", "dev", "--name", "agent-run", "--ttl", "1h"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if take.ExitCode != 0 {
			t.Fatalf("take: exit %d: %s", take.ExitCode, take.Combined)
		}
		result := erun.Run(t, []string{"activity", "stop-ready", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode == 0 {
			t.Fatalf("expected a leased env to refuse the stop, got exit 0:\n%s", result.Combined)
		}
		golden.Equal(t, "activity/stop_ready_blocked_by_a_held_lease", normalize.Apply(result.Combined))
		if _, err := os.Stat(filepath.Join(setup.Home, ".erun", "team", "dev", "stop-pending.json")); !os.IsNotExist(err) {
			t.Errorf("a leased env must not arm a grace window, stat err: %v", err)
		}
	})

	t.Run("stop_ready_blocked_by_a_held_lease_outside_working_hours", func(t *testing.T) {
		// The lease clause is absolute: outside working hours the quiet signals
		// stop holding the environment up, but a held lease still does, or an
		// agent run that starts near the end of the window loses its environment
		// mid-job. Same refusal, same withheld grace window, other arm of the
		// working-hours branch.
		setup := env.New(t)
		seedManagedCloudTenantEnvWithIdle(t, setup, "team", "dev", outsideWorkingHoursIdleBlock)
		take := erun.Run(t, []string{"activity", "lease", "take", "--tenant", "team", "--environment", "dev", "--name", "agent-run", "--ttl", "1h"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if take.ExitCode != 0 {
			t.Fatalf("take: exit %d: %s", take.ExitCode, take.Combined)
		}
		result := erun.Run(t, []string{"activity", "stop-ready", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode == 0 {
			t.Fatalf("expected a leased env to refuse the stop outside working hours, got exit 0:\n%s", result.Combined)
		}
		golden.Equal(t, "activity/stop_ready_blocked_by_a_held_lease_outside_working_hours", normalize.Apply(result.Combined))
		if _, err := os.Stat(filepath.Join(setup.Home, ".erun", "team", "dev", "stop-pending.json")); !os.IsNotExist(err) {
			t.Errorf("a leased env must not arm a grace window, stat err: %v", err)
		}
	})

	t.Run("sample_records_activity_only_when_work_advances", func(t *testing.T) {
		// The uninstrumented-work fallback. Driven against a fixture process tree
		// because a real /proc says something different on every machine, and the
		// point of the CPU-delta rule is that residency alone is not work.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		procRoot := filepath.Join(setup.Cwd, "proc")
		writeSampleProcess(t, procRoot, 101, "java", 500, 900)
		// The same tick also retains a usage reading. Point it at an absent
		// cgroup root so this scenario stays about the process sampler and reads
		// nothing ambient from the host that ran it.
		sampleArgs := []string{"activity", "sample", "--tenant", "team", "--environment", "dev", "--proc-root", procRoot, "--cgroup-root", filepath.Join(setup.Cwd, "no-cgroup")}

		first := erun.Run(t, sampleArgs, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		writeSampleProcess(t, procRoot, 101, "java", 900, 900)
		working := erun.Run(t, sampleArgs, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		quiet := erun.Run(t, sampleArgs, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		golden.Equal(t, "activity/sample_records_activity_only_when_work_advances", normalize.Apply(
			first.Combined+working.Combined+quiet.Combined))

		// The recorded activity is the side effect the idle decision reads, and it
		// lives outside the captured streams.
		if _, err := os.Stat(filepath.Join(setup.CacheHome, "erun", "activity", "team", "dev", "process.json")); err != nil {
			t.Errorf("expected the working sample to record process activity: %v", err)
		}
	})

	t.Run("sample_records_ssh_activity_for_a_pty_holding_session", func(t *testing.T) {
		// A real interactive session is an sshd child ("sshd:
		// user@ptsN", itself tty-less) that forks a shell holding the allocated
		// pseudo-terminal. That must read as SSH activity even though nothing
		// crossed the host-side forward that this fixture cannot simulate.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		procRoot := filepath.Join(setup.Cwd, "proc")
		writeSampleProcessFull(t, procRoot, 401, 1, "sshd", 10, 900, 0)
		writeSampleProcessFull(t, procRoot, 403, 401, "bash", 10, 901, 34816)
		result := erun.Run(t, []string{"activity", "sample", "--tenant", "team", "--environment", "dev", "--proc-root", procRoot, "--cgroup-root", filepath.Join(setup.Cwd, "no-cgroup")}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		body, err := os.ReadFile(filepath.Join(setup.CacheHome, "erun", "activity", "team", "dev", "ssh.json"))
		if err != nil {
			t.Fatalf("expected the pty-holding session to record ssh activity: %v", err)
		}
		if !strings.Contains(string(body), `"lastActivity"`) {
			t.Errorf("expected lastActivity recorded for a real session, got:\n%s", body)
		}
	})

	t.Run("sample_ignores_a_notty_ssh_child", func(t *testing.T) {
		// An sshd child whose forked command never allocated a pty -
		// the shape a port-forward re-establishment or a background sync
		// channel takes - must never read as SSH activity, or the phantom-session
		// bug this fixes comes back.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		procRoot := filepath.Join(setup.Cwd, "proc")
		writeSampleProcessFull(t, procRoot, 402, 1, "sshd", 10, 900, 0)
		writeSampleProcessFull(t, procRoot, 404, 402, "sftp-server", 10, 901, 0)
		result := erun.Run(t, []string{"activity", "sample", "--tenant", "team", "--environment", "dev", "--proc-root", procRoot, "--cgroup-root", filepath.Join(setup.Cwd, "no-cgroup")}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if _, err := os.Stat(filepath.Join(setup.CacheHome, "erun", "activity", "team", "dev", "ssh.json")); !os.IsNotExist(err) {
			t.Errorf("expected no ssh activity recorded for a notty sshd child, stat err: %v", err)
		}
	})

	t.Run("sample_retains_runtime_usage_from_cgroup_counters", func(t *testing.T) {
		// The collection half of the sizing recommendation. The monitor's tick
		// already runs this command, so the history accumulates with no second
		// scheduled job.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		procRoot := filepath.Join(setup.Cwd, "proc")
		cgroupRoot := filepath.Join(setup.Cwd, "cgroup")
		writeCgroupFixture(t, cgroupRoot, cgroupV2Fixture(27551234478, 376556, 0, 12742377472, 0))
		sampleArgs := []string{"activity", "sample", "--tenant", "team", "--environment", "dev", "--proc-root", procRoot, "--cgroup-root", cgroupRoot}

		first := erun.Run(t, sampleArgs, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		writeCgroupFixture(t, cgroupRoot, cgroupV2Fixture(28551234478, 386556, 0, 12742377472, 0))
		second := erun.Run(t, sampleArgs, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		golden.Equal(t, "activity/sample_retains_runtime_usage_from_cgroup_counters", normalize.Apply(first.Combined+second.Combined))

		history := readUsageHistory(t, setup.CacheHome, "team", "dev")
		if got := history["observedPeakMemoryBytes"]; got != float64(12742377472) {
			t.Errorf("observedPeakMemoryBytes = %v, want the cgroup high-water 12742377472", got)
		}
		if got := history["observedPeriods"]; got != float64(386556) {
			t.Errorf("observedPeriods = %v, want the first lifetime's total plus the delta", got)
		}
		if samples, ok := history["samples"].([]any); !ok || len(samples) != 2 {
			t.Errorf("samples = %v, want both ticks retained", history["samples"])
		}
	})

	t.Run("sample_retains_the_peak_across_a_container_restart", func(t *testing.T) {
		// memory.peak resets when the container restarts, which is exactly why
		// this is a store and not a live read: without retention the pre-restart
		// high-water is simply gone, and an environment gets sized from whatever
		// it happens to have done since.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		procRoot := filepath.Join(setup.Cwd, "proc")
		cgroupRoot := filepath.Join(setup.Cwd, "cgroup")
		sampleArgs := []string{"activity", "sample", "--tenant", "team", "--environment", "dev", "--proc-root", procRoot, "--cgroup-root", cgroupRoot}

		writeCgroupFixture(t, cgroupRoot, cgroupV2Fixture(27551234478, 376556, 12, 22000000000, 1))
		erun.Run(t, sampleArgs, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		// Every cumulative counter starts over, and the fresh peak is far below
		// the one already observed.
		writeCgroupFixture(t, cgroupRoot, cgroupV2Fixture(4000, 40, 0, 900000000, 0))
		erun.Run(t, sampleArgs, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})

		history := readUsageHistory(t, setup.CacheHome, "team", "dev")
		if got := history["restarts"]; got != float64(1) {
			t.Errorf("restarts = %v, want 1 inferred from the counters going backwards", got)
		}
		if got := history["observedPeakMemoryBytes"]; got != float64(22000000000) {
			t.Errorf("observedPeakMemoryBytes = %v, want the pre-restart high-water 22000000000", got)
		}
		if got := history["observedOomKills"]; got != float64(1) {
			t.Errorf("observedOomKills = %v, want the pre-restart kill retained", got)
		}
		if got := history["observedThrottledPeriods"]; got != float64(12) {
			t.Errorf("observedThrottledPeriods = %v, want the pre-restart throttling retained", got)
		}
	})

	t.Run("sample_records_no_usage_when_the_host_supplies_no_counters", func(t *testing.T) {
		// cgroup v1, or a laptop. A history of empty samples could only ever
		// support "no evidence", so nothing is written at all.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		procRoot := filepath.Join(setup.Cwd, "proc")
		result := erun.Run(t, []string{"activity", "sample", "--tenant", "team", "--environment", "dev", "--proc-root", procRoot, "--cgroup-root", filepath.Join(setup.Cwd, "absent")},
			erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode != 0 {
			t.Fatalf("an absent cgroup must not fail the monitor tick: exit %d\n%s", result.ExitCode, result.Combined)
		}
		path := filepath.Join(setup.CacheHome, "erun", "activity", "team", "dev", "usage-history.json")
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("expected no usage history without counters, stat err = %v", err)
		}
	})

	t.Run("sample_requires_target", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"activity", "sample"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without target flags, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "activity/sample_requires_target", normalize.Apply(result.Combined))
	})

	t.Run("ssh_proxy_requires_tenant_and_environment", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"activity", "ssh-proxy", "--listen", "127.0.0.1:0", "--target", "127.0.0.1:1"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without tenant/environment, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "activity/ssh_proxy_requires_tenant_and_environment", normalize.Apply(result.Combined))
	})

	t.Run("ssh_proxy_requires_listen_and_target", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"activity", "ssh-proxy", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without addresses, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "activity/ssh_proxy_requires_listen_and_target", normalize.Apply(result.Combined))
	})

	t.Run("ssh_proxy_rejects_negative_idle_traffic_threshold", func(t *testing.T) {
		// A negative byte threshold is a misconfiguration, not "always idle".
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"activity", "ssh-proxy", "--tenant", "team", "--environment", "dev", "--listen", "127.0.0.1:0", "--target", "127.0.0.1:1", "--idle-traffic-bytes=-1"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for negative threshold, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "activity/ssh_proxy_rejects_negative_idle_traffic_threshold", normalize.Apply(result.Combined))
	})

	t.Run("ssh_proxy_rejects_unlistenable_address", func(t *testing.T) {
		// A listen address with no port fails deterministically (no DNS, no port
		// collision), so the listener-setup error path is locked without starting
		// the accept loop.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"activity", "ssh-proxy", "--tenant", "team", "--environment", "dev", "--listen", "127.0.0.1", "--target", "127.0.0.1:1"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for unlistenable address, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "activity/ssh_proxy_rejects_unlistenable_address", normalize.Apply(result.Combined))
	})
}

// TestActivityAISession exercises the ai-session verbs a tool's own
// turn-boundary hooks report through: the structured status replacing a
// guess made from PTY output volume. The load-bearing case is
// awaiting_input_survives_silence_and_is_not_idle_or_exited below - a PTY
// output-volume heuristic cannot distinguish "waiting on the human" from
// "idle" or "gone" because both produce the same signal, no output at all.
func TestActivityAISession(t *testing.T) {
	t.Parallel()
	report := func(t *testing.T, setup env.Setup, args ...string) erun.Result {
		t.Helper()
		full := append([]string{"activity", "ai-session", "report", "--tenant", "team", "--environment", "dev"}, args...)
		result := erun.Run(t, full, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("report %v: exit %d: %s", args, result.ExitCode, result.Combined)
		}
		return result
	}
	statusJSON := func(t *testing.T, setup env.Setup, args ...string) []map[string]any {
		t.Helper()
		full := append([]string{"activity", "ai-session", "status", "--tenant", "team", "--environment", "dev", "--json"}, args...)
		result := erun.Run(t, full, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("status %v: exit %d: %s", args, result.ExitCode, result.Combined)
		}
		var rows []map[string]any
		if err := json.Unmarshal([]byte(result.Stdout), &rows); err != nil {
			t.Fatalf("parse status --json: %v\n%s", err, result.Stdout)
		}
		return rows
	}

	t.Run("awaiting_input_survives_silence_and_is_not_idle_or_exited", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")

		report(t, setup, "--session", "sess-1", "--tool", "claude", "--event", "turn-start")
		busy := statusJSON(t, setup, "--session", "sess-1")
		if busy[0]["state"] != "busy" {
			t.Fatalf("after turn-start: want busy, got %v", busy[0]["state"])
		}

		// Reporting turn-end is the only thing that changes here: no further
		// process output ever arrives for this session, exactly like a real
		// session that is genuinely waiting on the human. A volume/silence
		// heuristic would read this as idle; the structured status must not.
		report(t, setup, "--session", "sess-1", "--event", "turn-end")
		awaiting := statusJSON(t, setup, "--session", "sess-1")
		if awaiting[0]["state"] != "awaiting-input" {
			t.Fatalf("after turn-end with no further output: want awaiting-input, got %v", awaiting[0]["state"])
		}

		// A session that never reported anything is genuinely idle, and must
		// read differently from the one silently awaiting input above.
		neverStarted := statusJSON(t, setup, "--session", "never-started")
		if neverStarted[0]["state"] != "idle" {
			t.Fatalf("session with no recorded event: want idle, got %v", neverStarted[0]["state"])
		}
		if awaiting[0]["state"] == neverStarted[0]["state"] {
			t.Fatalf("awaiting-input must not collapse into idle")
		}

		// Once the process actually exits, the state changes again and must
		// not be confused with the awaiting-input state that preceded it.
		report(t, setup, "--session", "sess-1", "--event", "exit", "--exit-code", "0")
		exited := statusJSON(t, setup, "--session", "sess-1")
		if exited[0]["state"] != "exited" {
			t.Fatalf("after exit: want exited, got %v", exited[0]["state"])
		}
	})

	t.Run("notify_also_resolves_to_awaiting_input", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		report(t, setup, "--session", "sess-2", "--tool", "codex", "--event", "notify")
		status := statusJSON(t, setup, "--session", "sess-2")
		if status[0]["state"] != "awaiting-input" {
			t.Fatalf("after notify: want awaiting-input, got %v", status[0]["state"])
		}
	})

	t.Run("exit_with_oom_reason_reports_oom_killed_distinct_from_plain_exit", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		report(t, setup, "--session", "oom-session", "--event", "exit", "--exit-code", "137", "--exit-reason", "oom")
		report(t, setup, "--session", "plain-session", "--event", "exit", "--exit-code", "1")
		oom := statusJSON(t, setup, "--session", "oom-session")
		plain := statusJSON(t, setup, "--session", "plain-session")
		if oom[0]["state"] != "oom-killed" {
			t.Fatalf("exit with oom reason: want oom-killed, got %v", oom[0]["state"])
		}
		if plain[0]["state"] != "exited" {
			t.Fatalf("plain exit: want exited, got %v", plain[0]["state"])
		}
		if oom[0]["state"] == plain[0]["state"] {
			t.Fatalf("an OOM kill must be distinguishable from an ordinary exit")
		}
	})

	t.Run("status_lists_every_recorded_session_when_none_named", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		report(t, setup, "--session", "b-session", "--tool", "codex", "--event", "turn-start")
		report(t, setup, "--session", "a-session", "--tool", "claude", "--event", "turn-end")
		rows := statusJSON(t, setup)
		if len(rows) != 2 {
			t.Fatalf("want 2 sessions listed, got %d: %v", len(rows), rows)
		}
		if rows[0]["sessionId"] != "a-session" || rows[1]["sessionId"] != "b-session" {
			t.Fatalf("expected sessions sorted by id, got %v", rows)
		}
	})

	// status_json_reports_empty_array_when_none_recorded pins the actual JSON
	// text emitted for an environment with no recorded sessions: it must be
	// "[]", never "null" (erun#2128) - a caller doing result.length or
	// ranging over the field must not have to special-case this one command.
	// json.Unmarshal happily decodes "null" into a nil slice with no error,
	// so statusJSON's []map[string]any helper cannot tell the two apart;
	// this scenario asserts on the raw stdout text instead.
	t.Run("status_json_reports_empty_array_when_none_recorded", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"activity", "ai-session", "status", "--tenant", "team", "--environment", "dev", "--json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("status --json on an untouched environment: exit %d: %s", result.ExitCode, result.Combined)
		}
		if got := strings.TrimSpace(result.Stdout); got != "[]" {
			t.Fatalf("want status --json to print [] for no recorded sessions, got %q", got)
		}
	})

	t.Run("report_rejects_unsupported_event", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"activity", "ai-session", "report", "--tenant", "team", "--environment", "dev", "--session", "sess-1", "--event", "bogus"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for unsupported event, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "activity/ai_session_report_rejects_unsupported_event", normalize.Apply(result.Combined))
	})
}
