package integration

import (
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestAPI(t *testing.T) {
	t.Parallel()
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"api", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "api/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_traces_eapi_launch", func(t *testing.T) {
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
		// A second tenant puts "team" at index 1: 17000 + 100 + 33 = 17133.
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
		// eapi only spawns past the dry-run gate; the stub echoes its argv.
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
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"api", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when eapi is missing, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "api/real_run_errors_when_eapi_missing", normalize.Apply(result.Combined))
	})

	t.Run("real_run_propagates_eapi_exit_failure", func(t *testing.T) {
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
