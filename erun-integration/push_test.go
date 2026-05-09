package integration

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestPush(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		// Seed a Dockerfile in the test cwd so the root `erun push`
		// shorthand registers — without it, `push --help` falls through
		// to the root help and the push-specific flags (notably --force)
		// are not visible in the golden.
		setup := env.New(t)
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedProjectDockerfile(t, setup)
		result := erun.Run(t, []string{"push", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Stdout, "--force") {
			t.Fatalf("expected push help to advertise --force, got:\n%s", result.Combined)
		}
		golden.Equal(t, "push/help", normalize.Apply(result.Combined))
	})
}
