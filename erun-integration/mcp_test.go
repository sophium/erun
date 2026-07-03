package integration

import (
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestMCP(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"mcp", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "mcp/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_traces_emcp_launch", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		args := []string{
			"-v", "mcp", "team", "dev", "--dry-run",
			"--host", "0.0.0.0",
			"--port", "17001",
			"--path", "custom",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "mcp/dry_run_traces_emcp_launch", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_uses_environment_local_port_by_default", func(t *testing.T) {
		// Seeding alpha first pushes "team" to index 1, so the default port
		// resolves to 17100 (17000 + 100), not the index-0 17000 — the seed
		// order proves the port is environment-scoped.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "alpha", "dev")
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"-v", "mcp", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "mcp/dry_run_uses_environment_local_port_by_default", normalize.Apply(result.Combined))
	})

	t.Run("real_run_launches_emcp_stub", func(t *testing.T) {
		// Real-run: the launcher body only executes past the dry-run gate,
		// so a stub is the only way to reach the bare-name emcp resolution
		// and lock the argv it launches.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "emcp", `printf 'emcp stub argv: %s\n' "$*"
exit 0`)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "emcp")...)
		result := erun.Run(t, []string{"-vv", "mcp", "team", "dev", "--port", "17001"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "mcp/real_run_launches_emcp_stub", normalize.Apply(result.Combined))
	})

	t.Run("real_run_errors_when_emcp_missing", func(t *testing.T) {
		// A missing emcp must surface the friendly "build or install it
		// first" message, not a raw exec error.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := append(setup.Env(), emptyPathDir(t, setup.Cwd))
		result := erun.Run(t, []string{"mcp", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when emcp is missing, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "mcp/real_run_errors_when_emcp_missing", normalize.Apply(result.Combined))
	})

	t.Run("real_run_propagates_emcp_exit_failure", func(t *testing.T) {
		// A launched emcp that exits non-zero must propagate its raw exit
		// error and the tool's stderr (not the friendly missing-binary
		// message).
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "emcp", `printf 'emcp stub failing\n' >&2
exit 3`)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "emcp")...)
		result := erun.Run(t, []string{"mcp", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when emcp fails, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "mcp/real_run_propagates_emcp_exit_failure", normalize.Apply(result.Combined))
	})
}
