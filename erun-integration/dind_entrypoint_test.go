package integration

import (
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestDindEntrypoint(t *testing.T) {
	t.Parallel()

	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"dind-entrypoint", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "dind-entrypoint/help", normalize.Apply(result.Combined))
	})

	t.Run("refuses_a_host_that_is_not_linux", func(t *testing.T) {
		// Linux-only; pinned to darwin so the golden holds on every host.
		setup := env.New(t)
		result := erun.Run(t, []string{"dind-entrypoint", "--", "--insecure-registry", "registry.cluster.local:5000"}, erun.RunOptions{
			Cwd: setup.Cwd,
			Env: append(setup.Env(), "ERUN_HOST_OS_OVERRIDE=darwin"),
		})
		if result.ExitCode == 0 {
			t.Fatalf("expected a refusal off Linux, got exit 0:\n%s", result.Combined)
		}
		golden.Equal(t, "dind-entrypoint/refuses_a_host_that_is_not_linux", normalize.Apply(result.Combined))
	})
}
