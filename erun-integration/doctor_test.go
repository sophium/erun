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

func TestDoctor(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"doctor", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_prune_images_traces_dind_exec", func(t *testing.T) {
		// Exercises doctor.go --prune-images action: --dry-run must trace
		// the kubectl wait + the dind exec command line that would prune
		// docker images, including the resolved kubernetes context and
		// namespace, without performing any side effect.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		for _, want := range []string{
			"kubectl --context test-context --namespace team-dev wait --for=condition=Available --timeout 2m0s deployment/team-devops",
			"kubectl --context test-context --namespace team-dev exec -c erun-dind deployment/team-devops",
			"docker image prune -a -f",
		} {
			if !strings.Contains(result.Stderr, want) {
				t.Errorf("expected trace to contain %q, got stderr:\n%s", want, result.Stderr)
			}
		}
	})
}
