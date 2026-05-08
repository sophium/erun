package integration

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestVersion(t *testing.T) {
	t.Run("no_registry", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"version", "--no-registry"}, erun.RunOptions{
			Cwd: setup.Cwd,
			Env: setup.Env(),
		})
		if result.ExitCode != 0 {
			t.Fatalf("erun version --no-registry exited %d:\n%s", result.ExitCode, result.Combined)
		}
		if !strings.HasPrefix(strings.TrimSpace(result.Stdout), "erun ") {
			t.Errorf("expected stdout to start with 'erun ', got:\n%s", result.Stdout)
		}
		golden.Equal(t, "version/no_registry", normalize.Apply(result.Combined))
	})
}
