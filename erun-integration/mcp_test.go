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

	t.Run("real_run_launches_emcp_stub", func(t *testing.T) {
		// Exercises launchMCPProcess + resolveMCPExecutable's bare-name
		// fallthrough: no emcp sibling exists next to the harness-built erun
		// binary, so resolution falls through to "emcp" and
		// eruncommon.Command routes the spawn to the stub via ERUN_EMCP_BIN.
		// Real-run because the launcher body only executes past the dry-run
		// gate; the stub echoes its argv so the golden locks the launched
		// command line next to the -vv trace of the same argv.
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
		// launchMCPProcess's exec.ErrNotFound branch: with no ERUN_EMCP_BIN
		// override and PATH pointing at an empty directory, the lookup fails
		// and the launcher must surface the friendly "build or install it
		// first" message instead of a raw exec error.
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
		// launchMCPProcess's generic error branch: a launched emcp that
		// exits non-zero is not exec.ErrNotFound, so the raw "exit status"
		// error must propagate to the user together with the tool's stderr.
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
