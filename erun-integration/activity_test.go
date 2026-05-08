package integration

import (
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
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"activity", "status", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "activity/status_with_seeded_env", normalize.Apply(result.Combined))
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
}
