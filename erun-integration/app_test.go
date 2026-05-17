package integration

import (
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestApp(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"app", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "app/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_traces_app_executable_without_launching", func(t *testing.T) {
		// Exercises app.go: --dry-run must trace the resolved erun-app
		// executable path and short-circuit before launching the desktop
		// process. The trace presence is the contract; the absence of any
		// child process is enforced by the dry-run gate.
		setup := env.New(t)
		result := erun.Run(t, []string{"app", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "app/dry_run_traces_app_executable_without_launching", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_traces_headless_flags_for_app_executable", func(t *testing.T) {
		// --headless / --port are forwarded to erun-app so a headless
		// browser harness can drive the same frontend. The dry-run
		// trace must show them as part of the resolved launch command
		// so reviewers can audit what would have been spawned.
		setup := env.New(t)
		result := erun.Run(t, []string{"app", "--headless", "--port", "34123", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "app/dry_run_traces_headless_flags_for_app_executable", normalize.Apply(result.Combined))
	})
}
