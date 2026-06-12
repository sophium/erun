package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
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

	t.Run("clone_real_run_fresh_clones_and_installs_shim", func(t *testing.T) {
		// Real-run fresh-clone arm of RunContributeCloneWithIO: the parent
		// dir is created, `git clone` runs (routed through a git stub via
		// ERUN_GIT_BIN — the suite has no network), the "Cloned" audit line
		// prints, and installContributeShim writes the PATH-shim wrapper
		// script for real. The shim body is asserted directly: it must
		// resolve $HOME at exec time, not embed the install-time home.
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "git", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "git")...)
		result := erun.Run(t, []string{"contribute", "clone"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "contribute/clone_real_run_fresh_clones_and_installs_shim", normalize.Apply(result.Combined))
		shim, err := os.ReadFile(filepath.Join(setup.Home, ".erun", "contribute", "bin", "erun"))
		if err != nil {
			t.Fatalf("read contribute shim (installContributeShim did not write it): %v", err)
		}
		if want := "exec \"$HOME/git/erun/erun-cli/run.sh\" \"$@\"\n"; !strings.Contains(string(shim), want) {
			t.Errorf("expected shim to forward to the clone's run.sh via $HOME, got:\n%s", shim)
		}
	})

	t.Run("clone_real_run_already_present_reinstalls_shim", func(t *testing.T) {
		// Real-run already-present arm: with ~/git/erun pointing at the
		// canonical remote, the clone is skipped ("already present" audit
		// line, no git clone) but the shim is still (re)installed so a
		// stale or missing shim heals on every clone invocation. Uses the
		// real git binary for `remote get-url origin` against the seeded
		// checkout.
		setup := env.New(t)
		seedExistingContributeClone(t, filepath.Join(setup.Home, "git", "erun"), "https://github.com/sophium/erun.git")
		result := erun.Run(t, []string{"contribute", "clone"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "contribute/clone_real_run_already_present_reinstalls_shim", normalize.Apply(result.Combined))
		if _, err := os.Stat(filepath.Join(setup.Home, ".erun", "contribute", "bin", "erun")); err != nil {
			t.Errorf("expected shim to be installed for an already-present clone: %v", err)
		}
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
