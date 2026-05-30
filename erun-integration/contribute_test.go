package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestContribute(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"contribute", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "contribute/help", normalize.Apply(result.Combined))
	})

	t.Run("clone_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"contribute", "clone", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "contribute/clone_help", normalize.Apply(result.Combined))
	})

	t.Run("clone_dry_run_fresh", func(t *testing.T) {
		// Dry-run trace when ~/git/erun does not exist: command emits the
		// audit line, the mkdir trace for $HOME/git, and the git clone
		// command line. No side effect on disk.
		setup := env.New(t)
		result := erun.Run(t, []string{"contribute", "clone", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "contribute/clone_dry_run_fresh", normalize.Apply(result.Combined))
		if _, err := os.Stat(filepath.Join(setup.Home, "git", "erun")); !os.IsNotExist(err) {
			t.Fatalf("dry-run must not create the clone: %v", err)
		}
	})

	t.Run("clone_dry_run_already_present", func(t *testing.T) {
		// Dry-run trace when ~/git/erun is already a checkout whose origin
		// remote points at the canonical ERun repository. Command short-
		// circuits with the "already present" audit line; no mkdir or git
		// clone trace is emitted.
		setup := env.New(t)
		seedExistingContributeClone(t, filepath.Join(setup.Home, "git", "erun"), "git@github.com:sophium/erun.git")
		result := erun.Run(t, []string{"contribute", "clone", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "contribute/clone_dry_run_already_present", normalize.Apply(result.Combined))
	})

	t.Run("clone_dry_run_wrong_remote", func(t *testing.T) {
		// When ~/git/erun exists but its origin remote points elsewhere,
		// the command refuses to proceed with a descriptive error rather
		// than overwriting the user's checkout.
		setup := env.New(t)
		seedExistingContributeClone(t, filepath.Join(setup.Home, "git", "erun"), "git@github.com:other/repo.git")
		result := erun.Run(t, []string{"contribute", "clone", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when remote is wrong; got %s", result.Combined)
		}
		golden.Equal(t, "contribute/clone_dry_run_wrong_remote", normalize.Apply(result.Combined))
	})
}

// seedExistingContributeClone bootstraps a tiny git repository at dir
// with the given origin URL so dry-run scenarios can exercise the
// existing-clone code path without a network round-trip.
func seedExistingContributeClone(t testing.TB, dir, originURL string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example")
	run("config", "user.name", "Test")
	run("remote", "add", "origin", originURL)
}
