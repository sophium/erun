package integration

import (
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestRoot(t *testing.T) {
	t.Parallel()
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
		// Covers the interactive tenant-selection prompt. Seeding two
		// tenants with no default, plus a cwd outside both project roots,
		// forces the bare command to fall through to the select prompt,
		// which only scripted stdin can drive under the non-TTY harness.
		setup := env.New(t)
		seedTenantWithoutDefault(t, setup, "alpha", "dev")
		seedTenantWithoutDefault(t, setup, "beta", "dev")
		result := erun.Run(t, []string{"--dry-run"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "2\n",
		})
		golden.Equal(t, "root/tenant_select_via_stdin", normalize.Apply(result.Combined))
	})

	t.Run("tenant_select_initialize_current_project_via_stdin", func(t *testing.T) {
		// Covers the select prompt's "Initialize current project" arm.
		// The cwd is not a git repo, so the bootstrap must stop with the
		// "erun config is not initialized" guidance rather than initialize;
		// only scripted stdin can reach this arm.
		setup := env.New(t)
		seedTenantWithoutDefault(t, setup, "alpha", "dev")
		seedTenantWithoutDefault(t, setup, "beta", "dev")
		result := erun.Run(t, []string{"--dry-run"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "3\n",
		})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "root/tenant_select_initialize_current_project_via_stdin", normalize.Apply(result.Combined))
	})
}
