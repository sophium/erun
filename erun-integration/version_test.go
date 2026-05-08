package integration

import (
	"os"
	"path/filepath"
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

	t.Run("time_flag_prints_elapsed", func(t *testing.T) {
		// Exercises feedback_render.go printElapsedTime: the --time flag must
		// emit an "elapsed:" line on stderr after a successful run.
		setup := env.New(t)
		result := erun.Run(t, []string{"version", "--no-registry", "--time"}, erun.RunOptions{
			Cwd: setup.Cwd,
			Env: setup.Env(),
		})
		if result.ExitCode != 0 {
			t.Fatalf("erun version --time exited %d:\n%s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Stderr, "elapsed:") {
			t.Errorf("expected stderr to contain 'elapsed:', got:\n%s", result.Stderr)
		}
	})

	t.Run("version_file_in_cwd_overrides_build_info", func(t *testing.T) {
		// Exercises version.go resolveBuildInfo: when a VERSION file lives
		// in the current working directory, its contents must replace the
		// compiled-in version string in the output.
		setup := env.New(t)
		if err := os.WriteFile(filepath.Join(setup.Cwd, "VERSION"), []byte("9.9.9\n"), 0o644); err != nil {
			t.Fatalf("write VERSION: %v", err)
		}
		result := erun.Run(t, []string{"version", "--no-registry"}, erun.RunOptions{
			Cwd: setup.Cwd,
			Env: setup.Env(),
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.HasPrefix(strings.TrimSpace(result.Stdout), "erun 9.9.9") {
			t.Errorf("expected stdout to start with 'erun 9.9.9', got:\n%s", result.Stdout)
		}
	})

	t.Run("verbose_flag_prints_audit", func(t *testing.T) {
		// Exercises feedback_render.go auditCommand: with -v but without
		// --dry-run, the audit line must appear on stderr.
		setup := env.New(t)
		result := erun.Run(t, []string{"version", "--no-registry", "-v"}, erun.RunOptions{
			Cwd: setup.Cwd,
			Env: setup.Env(),
		})
		if result.ExitCode != 0 {
			t.Fatalf("erun version -v exited %d:\n%s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Stderr, "audit: erun version") {
			t.Errorf("expected audit line on stderr, got:\n%s", result.Stderr)
		}
	})
}
