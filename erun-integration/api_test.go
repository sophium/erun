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
}
