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
		// Exercises mcp.go: --dry-run must trace the exact emcp command line
		// (host, port, path, tenant, environment, repo-path, k8s context,
		// namespace) without starting the server.
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
		// Exercises mcp.go default-port resolution: when invoked with a
		// tenant/environment but no --port, the trace must show the
		// environment-scoped local MCP port. Two tenants force "team" to
		// index 1, so the MCP port is 17000 + 100 = 17100 rather than the
		// index-0 17000.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "alpha", "dev")
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"-v", "mcp", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "mcp/dry_run_uses_environment_local_port_by_default", normalize.Apply(result.Combined))
	})
}
