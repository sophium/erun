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

	t.Run("raw_dry_run_traces_inside_project", func(t *testing.T) {
		// Exercises eruncommon.RunRawCommand: with a real project root
		// resolved, the dry-run trace must show the resolved cwd and the
		// raw command before the runner short-circuits.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "raw", "echo", "hello", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "exec/raw_dry_run_traces_inside_project", normalize.Apply(result.Combined))
	})

	t.Run("raw_dry_run_redacts_sensitive_args", func(t *testing.T) {
		// Exercises feedback_render.go redactAuditArgs and
		// eruncommon.RunRawCommand argument redaction: --token and
		// --password values must appear as <redacted> in both the audit
		// line and the raw-command trace line.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "raw", "--dry-run", "curl", "https://example", "--token", "secret-value", "--password=hidden", "ok"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "exec/raw_dry_run_redacts_sensitive_args", normalize.Apply(result.Combined))
	})

	t.Run("diff_dry_run_traces_git_diff", func(t *testing.T) {
		// Exercises exec.go runExecDiffCommand: --dry-run must trace the
		// `git diff --no-color --no-ext-diff` command line for the resolved
		// project root.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "diff", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "exec/diff_dry_run_traces_git_diff", normalize.Apply(result.Combined))
	})

	t.Run("diff_dry_run_errors_outside_git_project", func(t *testing.T) {
		// Exercises exec.go runExecDiffCommand error path: outside a git
		// project, findProjectRoot fails and the audit line surfaces the
		// `cannot find git project` message before any side effect runs.
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "diff", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit outside a git project, got 0:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "cannot find git project") {
			t.Errorf("expected 'cannot find git project' message, got:\n%s", result.Combined)
		}
	})

	t.Run("dry_run_with_time_flag_prints_elapsed_on_error", func(t *testing.T) {
		// Exercises feedback_render.go printElapsedTime error path: when
		// --time is set and the command fails, the `elapsed:` line must
		// still appear on stderr. Driving this through `exec diff --dry-run`
		// outside a git project keeps the run side-effect-free.
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "diff", "--dry-run", "--time"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit (no git project), got 0:\n%s", result.Combined)
		}
		if !strings.Contains(result.Stderr, "elapsed:") {
			t.Errorf("expected --time to print elapsed even on error, got stderr:\n%s", result.Stderr)
		}
	})
}
