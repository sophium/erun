package integration

import (
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
}
