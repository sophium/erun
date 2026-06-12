package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// emptyPathDir creates an empty directory and returns a PATH= env entry
// pointing at it. os/exec deduplicates Env keeping the last value, so
// appending the entry after setup.Env() overrides the inherited PATH and
// makes "binary not on PATH" deterministic regardless of what the host has
// installed.
func emptyPathDir(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "empty-path")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	return "PATH=" + dir
}

func TestAPI(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"api", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "api/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_traces_eapi_launch", func(t *testing.T) {
		// Exercises api.go: --dry-run must trace the exact eapi command line
		// (host, port, database-url, redacting only secrets) without
		// starting the server.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		args := []string{
			"-v", "api", "team", "dev", "--dry-run",
			"--host", "0.0.0.0",
			"--port", "17034",
			"--database-url", "postgres://erun@example/erun",
			"--oidc-allowed-issuers", "https://issuer.example",
			"--aws-identity-store-id", "d-1234567890",
			"--aws-identity-store-region", "eu-west-2",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "api/dry_run_traces_eapi_launch", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_uses_environment_local_port_by_default", func(t *testing.T) {
		// Exercises api.go default-port resolution: when invoked with a
		// tenant/environment but no --port, the trace must show the
		// environment-scoped local API port. Two tenants force "team" to
		// index 1, so the API port is 17000 + 100 + APIServicePortOffset
		// (33) = 17133 rather than the index-0 17033.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "alpha", "dev")
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"-v", "api", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "api/dry_run_uses_environment_local_port_by_default", normalize.Apply(result.Combined))
	})

	t.Run("real_run_launches_eapi_stub", func(t *testing.T) {
		// Exercises launchAPIProcess + resolveAPIExecutable's bare-name
		// fallthrough: no eapi sibling exists next to the harness-built erun
		// binary, so resolution falls through to "eapi" and
		// eruncommon.Command routes the spawn to the stub via ERUN_EAPI_BIN.
		// Real-run (no --dry-run) because the launcher body only executes
		// past the dry-run gate; the stub echoes its argv so the golden
		// locks the launched command line next to the -vv trace of the same
		// argv.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "eapi", `printf 'eapi stub argv: %s\n' "$*"
exit 0`)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "eapi")...)
		result := erun.Run(t, []string{"-vv", "api", "team", "dev", "--port", "17034"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "api/real_run_launches_eapi_stub", normalize.Apply(result.Combined))
	})

	t.Run("real_run_errors_when_eapi_missing", func(t *testing.T) {
		// launchAPIProcess's exec.ErrNotFound branch: with no ERUN_EAPI_BIN
		// override and PATH pointing at an empty directory, the lookup fails
		// and the launcher must surface the friendly "build or install it
		// first" message instead of a raw exec error.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := append(setup.Env(), emptyPathDir(t, setup.Cwd))
		result := erun.Run(t, []string{"api", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when eapi is missing, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "api/real_run_errors_when_eapi_missing", normalize.Apply(result.Combined))
	})

	t.Run("real_run_propagates_eapi_exit_failure", func(t *testing.T) {
		// launchAPIProcess's generic error branch: a launched eapi that
		// exits non-zero is not exec.ErrNotFound, so the raw "exit status"
		// error must propagate to the user together with the tool's stderr.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "eapi", `printf 'eapi stub failing\n' >&2
exit 3`)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "eapi")...)
		result := erun.Run(t, []string{"api", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when eapi fails, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "api/real_run_propagates_eapi_exit_failure", normalize.Apply(result.Combined))
	})
}
