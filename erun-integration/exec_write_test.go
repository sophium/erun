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
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testExecWriteHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"exec", "write", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/write_help", normalize.Apply(result.Combined))
}

func testExecWriteDryRunTracesResolvedPath(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "write", "config/values.yaml", "--dry-run"}, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   setup.Env(),
		Stdin: "hello\n",
	})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/write_dry_run_traces_resolved_path", normalize.Apply(result.Combined))
	if _, err := os.Stat(filepath.Join(setup.Cwd, "config", "values.yaml")); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not write anything")
	}
}

func testExecWriteDryRunErrorsOutsideGitProject(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"exec", "write", "foo.txt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "x"})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit outside a git project, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/write_dry_run_errors_outside_git_project", normalize.Apply(result.Combined))
}

func testExecWriteRefusesPathOutsideProjectRoot(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "write", "../escape.txt"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "x"})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a path outside the project root, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/write_refuses_path_outside_project_root", normalize.Apply(result.Combined))
	if _, err := os.Stat(filepath.Join(filepath.Dir(setup.Cwd), "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected no file to land outside the project root")
	}
}

func testExecWriteRefusesSymlinkLeafPointingOutsideTree(t *testing.T) {
	// The whole justification for `exec write` over `raw` is that a write
	// can only land inside the working tree. A lexical containment check
	// does not follow symlinks, so a symlink inside the tree pointing
	// anywhere on the filesystem would defeat it. Assert on the actual
	// filesystem outcome, not just the exit code: the defect is that the
	// check returns the wrong answer, so a check-only assertion would not
	// have caught it.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	outside := t.TempDir()
	target := filepath.Join(outside, "pwned.txt")
	if err := os.Symlink(target, filepath.Join(setup.Cwd, "escape.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	result := erun.Run(t, []string{"exec", "write", "escape.txt"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "pwned\n"})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit writing through a symlink, got 0:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "symlink") {
		t.Fatalf("expected the refusal to mention the symlink, got:\n%s", result.Combined)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("expected no file to land at the symlink target, got err=%v", err)
	}
}

func testExecWriteRefusesWriteThroughSymlinkedDirectoryComponent(t *testing.T) {
	// Same escape, one level up: the symlink is a directory component of
	// the requested path rather than the leaf itself.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(setup.Cwd, "escape")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	result := erun.Run(t, []string{"exec", "write", "escape/pwned.txt"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "pwned\n"})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit writing through a symlinked directory, got 0:\n%s", result.Combined)
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected no file to land outside the working tree through the symlinked directory")
	}
}

func testExecWriteRootItselfSymlinkStillWritesInsideRealTree(t *testing.T) {
	// The root the write is confined to may itself be a symlink (e.g. a
	// tempdir alias); containment must be decided against the real
	// directory, and the write must still land there.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	rootLink := setup.Cwd + "-link"
	if err := os.Symlink(setup.Cwd, rootLink); err != nil {
		t.Fatalf("symlink root: %v", err)
	}
	envVars := append(setup.Env(), "ERUN_REPO_PATH="+rootLink)
	result := erun.Run(t, []string{"exec", "write", "values.yaml"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars, Stdin: "ok\n"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	got, err := os.ReadFile(filepath.Join(setup.Cwd, "values.yaml"))
	if err != nil {
		t.Fatalf("read written file via the real root: %v", err)
	}
	if string(got) != "ok\n" {
		t.Fatalf("content = %q, want %q", got, "ok\n")
	}
}

func testExecWriteRefusesAbsolutePathOutsideRoot(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	outside := filepath.Join(t.TempDir(), "escape.txt")
	result := erun.Run(t, []string{"exec", "write", outside}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "x"})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an absolute path outside the project root, got 0:\n%s", result.Combined)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("expected no file to land outside the project root")
	}
}

func testExecWriteRefusesPathOutsideRootAfterDotdotNormalisation(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "write", "sub/../../escape.txt"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "x"})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a path escaping via nested ../.., got 0:\n%s", result.Combined)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(setup.Cwd), "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected no file to land outside the project root")
	}
}

func testExecWriteContentRoundTripsByteIdenticalWithDangerousShellConstructs(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "write", "notes/todo.txt"}, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   setup.Env(),
		Stdin: execDangerousContent,
	})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	got, err := os.ReadFile(filepath.Join(setup.Cwd, "notes", "todo.txt"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(got) != execDangerousContent {
		t.Fatalf("written content = %q, want byte-identical %q", got, execDangerousContent)
	}
}
