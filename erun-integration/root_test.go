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
}
