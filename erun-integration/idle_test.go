package integration

import (
	"regexp"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestIdle(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"idle", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "idle/help", normalize.Apply(result.Combined))
	})

	t.Run("status_for_seeded_env", func(t *testing.T) {
		// Exercises eruncommon.ResolveStoredEnvironmentIdleStatus and
		// erun-cli/cmd/idle.go's writeIdleStatus / idleMarkerValue. The
		// working-hours marker is wall-clock-dependent so we assert the
		// stable lines exactly and the variable lines structurally.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"idle", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		stableLines := []string{
			"timeout: 300 seconds",
			"seconds until stop: 0",
			"stop eligible: off",
			"stop blocked: environment is not cloud-managed",
			"ssh: idle",
			"api: idle",
			"mcp: idle",
			"cli: idle",
			"codex: idle",
		}
		for _, line := range stableLines {
			if !strings.Contains(result.Stdout, line) {
				t.Errorf("expected stdout to contain %q, got:\n%s", line, result.Stdout)
			}
		}
		// working-hours: idle (when wall clock is outside 08:00-20:00) OR
		// active (12345s) (when inside). Either form is acceptable.
		workingHours := regexp.MustCompile(`(?m)^\s*working-hours: (idle|active \(\d+s\))\s*$`)
		if !workingHours.MatchString(result.Stdout) {
			t.Errorf("expected working-hours marker line, got:\n%s", result.Stdout)
		}
	})

	t.Run("missing_env_errors", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"idle", "missing", "missing"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "idle/missing_env_errors", normalize.Apply(result.Combined))
	})
}
