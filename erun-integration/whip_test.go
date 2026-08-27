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

func TestWhip(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"whip", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "whip/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_no_targets_configured", func(t *testing.T) {
		// No environments and no orchestrators configured at all: the report
		// has nothing to name, and the command still exits clean.
		setup := env.New(t)
		result := erun.Run(t, []string{"whip", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "whip/dry_run_no_targets_configured", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_one_named_environment_not_open_reports_not_alive", func(t *testing.T) {
		// SeedTenantEnv never sets a local port range, so there is no local MCP
		// edge to reach -- exactly the state of an environment nobody has open
		// in the desktop right now. This must resolve to a "not alive" skip,
		// not a hard command failure: the environment not being open is an
		// ordinary, expected outcome of a fleet-wide whip.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"whip", "--tenant", "team", "--environment", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "whip/dry_run_one_named_environment_not_open_reports_not_alive", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_whips_every_configured_environment_and_orchestrator", func(t *testing.T) {
		// With neither --tenant nor --environment, whip fans out over every
		// configured environment (across every tenant) plus every persisted
		// orchestrator. Orchestrators are always reported unreachable from
		// this transport -- a CLI process has no channel into a desktop-held
		// PTY -- independent of whether any environment edge is reachable.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedTenantEnv(t, setup, "other", "staging")
		seedOrchestrator(t, setup, "eng-1", "Eng One")
		result := erun.Run(t, []string{"whip", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "whip/dry_run_whips_every_configured_environment_and_orchestrator", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_json_output", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedOrchestrator(t, setup, "eng-1", "Eng One")
		result := erun.Run(t, []string{"whip", "--dry-run", "--json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "whip/dry_run_json_output", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_only_tenant_given_refuses", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"whip", "--tenant", "team", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit with only --tenant given, got 0: %s", result.Combined)
		}
		golden.Equal(t, "whip/dry_run_only_tenant_given_refuses", normalize.Apply(result.Combined))
	})
}

// seedOrchestrator appends a persisted orchestrator definition to the isolated
// root config, mirroring what the desktop writes to ~/.erun/config.yaml (see
// erun-common/config.go's OrchestratorConfig) without exercising the desktop
// UI that normally creates one.
func seedOrchestrator(t testing.TB, setup env.Setup, id, name string) {
	t.Helper()
	path := filepath.Join(setup.ConfigHome, "erun", "config.yaml")
	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read root config %s: %v", path, err)
	}
	extra := "orchestrators:\n  - id: " + id + "\n    name: " + name + "\n"
	if err := os.WriteFile(path, append(existing, []byte(extra)...), 0o644); err != nil {
		t.Fatalf("write root config %s: %v", path, err)
	}
}
