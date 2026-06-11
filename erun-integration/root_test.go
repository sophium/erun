package integration

import (
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestRoot(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "root/help", normalize.Apply(result.Combined))
	})

	t.Run("no_args_no_config", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "root/no_args_no_config", normalize.Apply(result.Combined))
	})

	t.Run("unknown_subcommand", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"definitelynotacommand"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "root/unknown_subcommand", normalize.Apply(result.Combined))
	})

	t.Run("tenant_select_via_stdin", func(t *testing.T) {
		// Executes selectTenantPrompt (init.go) and the bootstrap's
		// resolveTenantFromSelection: two tenants are seeded without a
		// default-tenant root config and the cwd sits outside both project
		// roots, so the bare root command falls through to the interactive
		// tenant selection. Stdin "j\r" moves the highlight to the second
		// tenant and confirms it; the trace then shows the selected
		// tenant's env being (re)initialized. Scripted stdin is the honest
		// tool here: the scenario exists to execute the select-prompt body.
		setup := env.New(t)
		seedTenantWithoutDefault(t, setup, "alpha", "dev")
		seedTenantWithoutDefault(t, setup, "beta", "dev")
		result := erun.Run(t, []string{"--dry-run"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "j\r",
		})
		golden.Equal(t, "root/tenant_select_via_stdin", normalize.Apply(result.Combined))
	})

	t.Run("tenant_select_initialize_current_project_via_stdin", func(t *testing.T) {
		// Executes selectTenantPrompt's "Initialize current project" arm:
		// stdin "jj\r" walks past both seeded tenants onto the initialize
		// option. The cwd is not a git repository, so the bootstrap must
		// stop with the "erun config is not initialized" guidance instead
		// of initializing anything. Scripted stdin is the honest tool here:
		// the initialize arm only exists inside the interactive prompt.
		setup := env.New(t)
		seedTenantWithoutDefault(t, setup, "alpha", "dev")
		seedTenantWithoutDefault(t, setup, "beta", "dev")
		result := erun.Run(t, []string{"--dry-run"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "jj\r",
		})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "root/tenant_select_initialize_current_project_via_stdin", normalize.Apply(result.Combined))
	})
}
