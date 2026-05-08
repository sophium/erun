package integration

import (
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestExec(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/help", normalize.Apply(result.Combined))
	})

	t.Run("noop_dry_run", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "noop", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "exec/noop_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("raw_dry_run", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "raw", "echo", "hello", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "exec/raw_dry_run", normalize.Apply(result.Combined))
	})
}
