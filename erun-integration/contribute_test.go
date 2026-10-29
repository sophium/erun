package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/harnessexec"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestContribute(t *testing.T) {
	t.Parallel()
	t.Run("help", testContributeHelp)
	t.Run("clone_help", testContributeCloneHelp)
	t.Run("clone_dry_run_fresh", testContributeCloneDryRunFresh)
	t.Run("clone_dry_run_already_present", testContributeCloneDryRunAlreadyPresent)
	t.Run("clone_dry_run_wrong_remote", testContributeCloneDryRunWrongRemote)
	t.Run("clone_real_run_fresh_clones_and_installs_shim", testContributeCloneRealRunFreshClonesAndInstallsShim)
	t.Run("clone_real_run_already_present_reinstalls_shim", testContributeCloneRealRunAlreadyPresentReinstallsShim)
}

func testContributeHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"contribute", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "contribute/help", normalize.Apply(result.Combined))
}

func testContributeCloneHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"contribute", "clone", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "contribute/clone_help", normalize.Apply(result.Combined))
}

func testContributeCloneDryRunFresh(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"contribute", "clone", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "contribute/clone_dry_run_fresh", normalize.Apply(result.Combined))
	if _, err := os.Stat(filepath.Join(setup.Home, "git", "erun")); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not create the clone: %v", err)
	}
}

func testContributeCloneDryRunAlreadyPresent(t *testing.T) {
	setup := env.New(t)
	seedExistingContributeClone(t, filepath.Join(setup.Home, "git", "erun"), "git@github.com:sophium/erun.git")
	result := erun.Run(t, []string{"contribute", "clone", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "contribute/clone_dry_run_already_present", normalize.Apply(result.Combined))
}

func testContributeCloneDryRunWrongRemote(t *testing.T) {
	setup := env.New(t)
	seedExistingContributeClone(t, filepath.Join(setup.Home, "git", "erun"), "git@github.com:other/repo.git")
	result := erun.Run(t, []string{"contribute", "clone", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when remote is wrong; got %s", result.Combined)
	}
	golden.Equal(t, "contribute/clone_dry_run_wrong_remote", normalize.Apply(result.Combined))
}

func testContributeCloneRealRunFreshClonesAndInstallsShim(t *testing.T) {
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
}

func testContributeCloneRealRunAlreadyPresentReinstallsShim(t *testing.T) {
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
}

func seedExistingContributeClone(t testing.TB, dir, originURL string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	run := func(args ...string) {
		cmd := harnessexec.Command("git", args...)
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
