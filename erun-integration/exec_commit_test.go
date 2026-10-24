package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testExecCommitHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"exec", "commit", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/commit_help", normalize.Apply(result.Combined))
}

func testExecCommitDryRunVerifiesBranchAndTraces(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "commit", "main", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "fix the values typo\n"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/commit_dry_run_verifies_branch_and_traces", normalize.Apply(result.Combined))
	if log := captureGit(t, setup.Cwd, "log", "--oneline"); strings.Count(strings.TrimSpace(log), "\n") != 0 {
		t.Fatalf("dry-run must not commit anything, got log: %q", log)
	}
}

func testExecCommitDryRunRefusesBranchMismatch(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "commit", "not-main", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "message\n"})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a branch mismatch under --dry-run, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/commit_dry_run_refuses_branch_mismatch", normalize.Apply(result.Combined))
}

func testExecCommitRefusesBranchMismatch(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "commit", "not-main"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "message\n"})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a branch mismatch, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/commit_refuses_branch_mismatch", normalize.Apply(result.Combined))
	if log := captureGit(t, setup.Cwd, "log", "--oneline"); strings.Count(strings.TrimSpace(log), "\n") != 0 {
		t.Fatalf("expected no new commit after refusal, got log: %q", log)
	}
}

func testExecCommitCommitsDangerousMessageAndReportsResult(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	mustWriteFile(t, filepath.Join(setup.Cwd, "values.yaml"), "typo: fixed\n")
	result := erun.Run(t, []string{"exec", "commit", "main", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: execDangerousContent})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	var parsed gitx.CommitWorkingTreeResult
	if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
		t.Fatalf("decode --output json: %v\n%s", err, result.Stdout)
	}
	if parsed.Branch != "main" || parsed.Commit == "" {
		t.Fatalf("unexpected result: %+v", parsed)
	}
	if len(parsed.Files) != 1 || parsed.Files[0] != "values.yaml" {
		t.Fatalf("expected committed files [values.yaml], got %+v", parsed.Files)
	}
	message := captureGit(t, setup.Cwd, "log", "-1", "--format=%B")
	if !strings.Contains(message, "`echo pwned` $(echo pwned) \"quoted\" 'quoted'") {
		t.Fatalf("commit message lost dangerous content verbatim: %q", message)
	}
	if status := captureGit(t, setup.Cwd, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Fatalf("expected clean tree after commit, got: %q", status)
	}
}

func testExecCommitRefusesWhenNothingToCommit(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "commit", "main"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "message\n"})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for nothing to commit, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/commit_refuses_when_nothing_to_commit", normalize.Apply(result.Combined))
}

func testExecCommitScopedPathsRefusesUnrelatedDirtyFile(t *testing.T) {
	// A change outside the declared paths refuses the whole commit rather than being
	// silently dropped.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	mustWriteFile(t, filepath.Join(setup.Cwd, "values.yaml"), "typo: fixed\n")
	mustWriteFile(t, filepath.Join(setup.Cwd, "unrelated.txt"), "someone else's in-flight work\n")
	result := erun.Run(t, []string{"exec", "commit", "main", "values.yaml"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "fix the values typo\n"})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a scoped commit with unrelated changes present, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/commit_scoped_paths_refuses_unrelated_dirty_file", normalize.Apply(result.Combined))
	if log := captureGit(t, setup.Cwd, "log", "--oneline"); strings.Count(strings.TrimSpace(log), "\n") != 0 {
		t.Fatalf("expected no new commit after refusal, got log: %q", log)
	}
	if status := captureGit(t, setup.Cwd, "status", "--porcelain"); strings.TrimSpace(status) == "" {
		t.Fatalf("expected both files to remain uncommitted after refusal")
	}
}

func testExecCommitScopedPathsCommitsOnlyTheDeclaredFile(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	mustWriteFile(t, filepath.Join(setup.Cwd, "values.yaml"), "typo: fixed\n")
	result := erun.Run(t, []string{"exec", "commit", "main", "values.yaml", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "fix the values typo\n"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	var parsed gitx.CommitWorkingTreeResult
	if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
		t.Fatalf("decode --output json: %v\n%s", err, result.Stdout)
	}
	if len(parsed.Files) != 1 || parsed.Files[0] != "values.yaml" {
		t.Fatalf("expected committed files [values.yaml], got %+v", parsed.Files)
	}
}

func testExecCommitDryRunScopedPathsTracesNeutralizedAddArgs(t *testing.T) {
	// Each path is prefixed with "./" so a leading ":" is not pathspec magic.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	mustWriteFile(t, filepath.Join(setup.Cwd, "values.yaml"), "typo: fixed\n")
	result := erun.Run(t, []string{"exec", "commit", "main", "values.yaml", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "fix the values typo\n"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/commit_dry_run_scoped_paths_traces_neutralized_add_args", normalize.Apply(result.Combined))
}

func testExecCommitDryRunNamesFilesThatWouldBeCommitted(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	mustWriteFile(t, filepath.Join(setup.Cwd, "values.yaml"), "typo: fixed\n")
	result := erun.Run(t, []string{"exec", "commit", "main", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "fix the values typo\n"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/commit_dry_run_names_files_that_would_be_committed", normalize.Apply(result.Combined))
	if log := captureGit(t, setup.Cwd, "log", "--oneline"); strings.Count(strings.TrimSpace(log), "\n") != 0 {
		t.Fatalf("dry-run must not commit anything, got log: %q", log)
	}
}

func testExecCommitDryRunScopedPathsRefusesUnrelatedDirtyFile(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	mustWriteFile(t, filepath.Join(setup.Cwd, "values.yaml"), "typo: fixed\n")
	mustWriteFile(t, filepath.Join(setup.Cwd, "unrelated.txt"), "someone else's in-flight work\n")
	result := erun.Run(t, []string{"exec", "commit", "main", "values.yaml", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "fix the values typo\n"})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a scoped dry-run with unrelated changes present, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/commit_dry_run_scoped_paths_refuses_unrelated_dirty_file", normalize.Apply(result.Combined))
}

func testExecCommitScopedPathsRejectsBlankPathEntries(t *testing.T) {
	// A list of only blank paths is a caller bug, not "commit everything".
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	mustWriteFile(t, filepath.Join(setup.Cwd, "values.yaml"), "typo: fixed\n")
	mustWriteFile(t, filepath.Join(setup.Cwd, "unrelated.txt"), "someone else's in-flight work\n")
	result := erun.Run(t, []string{"exec", "commit", "main", "", ""}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "message\n"})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a paths list of blank entries, got 0:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "blank") {
		t.Fatalf("expected the refusal to call out the blank path entries, got:\n%s", result.Combined)
	}
	if log := captureGit(t, setup.Cwd, "log", "--oneline"); strings.Count(strings.TrimSpace(log), "\n") != 0 {
		t.Fatalf("expected no commit for a blank-paths scope, got log: %q", log)
	}
	if status := captureGit(t, setup.Cwd, "status", "--porcelain"); strings.TrimSpace(status) == "" {
		t.Fatalf("expected both files to remain uncommitted after refusal")
	}
}

func testExecCommitScopedPathsAcceptsDirectoryScope(t *testing.T) {
	// A directory scope covers every file beneath it.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	mustWriteFile(t, filepath.Join(setup.Cwd, "config", "values.yaml"), "typo: fixed\n")
	mustWriteFile(t, filepath.Join(setup.Cwd, "config", "extra.yaml"), "more: config\n")
	result := erun.Run(t, []string{"exec", "commit", "main", "config", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "fix config\n"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	var parsed gitx.CommitWorkingTreeResult
	if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
		t.Fatalf("decode --output json: %v\n%s", err, result.Stdout)
	}
	if len(parsed.Files) != 2 {
		t.Fatalf("expected 2 committed files under config/, got %+v", parsed.Files)
	}
	var foundExtra, foundValues bool
	for _, f := range parsed.Files {
		switch f {
		case "config/extra.yaml":
			foundExtra = true
		case "config/values.yaml":
			foundValues = true
		}
	}
	if !foundExtra || !foundValues {
		t.Fatalf("expected config/extra.yaml and config/values.yaml, got %+v", parsed.Files)
	}
}

func testExecCommitScopedPathsAcceptsNonAsciiFilename(t *testing.T) {
	// Git C-quotes non-ASCII paths; the guard compares the real filename.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	name := "café.txt"
	mustWriteFile(t, filepath.Join(setup.Cwd, name), "café content\n")
	result := erun.Run(t, []string{"exec", "commit", "main", name, "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "add café notes\n"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	var parsed gitx.CommitWorkingTreeResult
	if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
		t.Fatalf("decode --output json: %v\n%s", err, result.Stdout)
	}
	if len(parsed.Files) != 1 || parsed.Files[0] != name {
		t.Fatalf("expected committed files [%s], got %+v", name, parsed.Files)
	}
}

func testExecCommitSurfacesGitAddFailureOutput(t *testing.T) {
	// git's stderr must reach the caller.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "commit", "main", "missing.txt"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "message\n"})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a scoped path that matches nothing, got 0:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "did not match any files") {
		t.Fatalf("expected git's own failure output to surface, got:\n%s", result.Combined)
	}
}

func testExecCommitSurfacesGitCommitFailureOutput(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	hooksDir := filepath.Join(setup.Cwd, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}
	hook := "#!/bin/sh\necho 'custom hook rejection: no thanks' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(hooksDir, "pre-commit"), []byte(hook), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	mustWriteFile(t, filepath.Join(setup.Cwd, "values.yaml"), "typo: fixed\n")
	result := erun.Run(t, []string{"exec", "commit", "main"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "fix values\n"})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when the pre-commit hook rejects, got 0:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "custom hook rejection: no thanks") {
		t.Fatalf("expected the hook's stderr to surface, got:\n%s", result.Combined)
	}
}

func testExecCommitReportsTheBranchTheCommitActuallyLandedOn(t *testing.T) {
	// A hook may switch HEAD; the result reports where the commit landed.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	hooksDir := filepath.Join(setup.Cwd, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}
	hook := "#!/bin/sh\ngit checkout -q -b diverted\n"
	if err := os.WriteFile(filepath.Join(hooksDir, "pre-commit"), []byte(hook), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	mustWriteFile(t, filepath.Join(setup.Cwd, "values.yaml"), "typo: fixed\n")
	result := erun.Run(t, []string{"exec", "commit", "main", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "fix values\n"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	var parsed gitx.CommitWorkingTreeResult
	if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
		t.Fatalf("decode --output json: %v\n%s", err, result.Stdout)
	}
	if parsed.Branch != "diverted" {
		t.Fatalf("expected the result to report the branch the commit actually landed on (diverted), got %q", parsed.Branch)
	}
}

func testExecCommitRootNotGitToplevelIgnoresChangesOutsideRoot(t *testing.T) {
	// Changed files are read relative to root (--relative), so a change outside root
	// is out of scope.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	nested := filepath.Join(setup.Cwd, "nested")
	mustWriteFile(t, filepath.Join(nested, "a.txt"), "a\n")
	fixture.RunGit(t, setup.Cwd, "add", "nested/a.txt")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "seed nested")
	mustWriteFile(t, filepath.Join(nested, "a.txt"), "a\nedit\n")
	mustWriteFile(t, filepath.Join(setup.Cwd, "README.md"), "# test\nunrelated top-level edit\n")
	envVars := append(setup.Env(), "ERUN_REPO_PATH="+nested)
	result := erun.Run(t, []string{"exec", "commit", "main", "a.txt", "--output", "json"}, erun.RunOptions{Cwd: nested, Env: envVars, Stdin: "fix nested a\n"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	var parsed gitx.CommitWorkingTreeResult
	if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
		t.Fatalf("decode --output json: %v\n%s", err, result.Stdout)
	}
	if len(parsed.Files) != 1 || parsed.Files[0] != "a.txt" {
		t.Fatalf("expected committed files [a.txt], got %+v", parsed.Files)
	}
	if status := captureGit(t, setup.Cwd, "status", "--porcelain", "--", "README.md"); strings.TrimSpace(status) == "" {
		t.Fatalf("expected README.md's unrelated change to remain uncommitted")
	}
}

func testExecCommitRootNotGitToplevelStillRefusesUnrelatedChangeInsideRoot(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	nested := filepath.Join(setup.Cwd, "nested")
	mustWriteFile(t, filepath.Join(nested, "a.txt"), "a\n")
	mustWriteFile(t, filepath.Join(nested, "b.txt"), "b\n")
	fixture.RunGit(t, setup.Cwd, "add", "nested/a.txt", "nested/b.txt")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "seed nested")
	mustWriteFile(t, filepath.Join(nested, "a.txt"), "a\nedit\n")
	mustWriteFile(t, filepath.Join(nested, "b.txt"), "b\nedit\n")
	envVars := append(setup.Env(), "ERUN_REPO_PATH="+nested)
	result := erun.Run(t, []string{"exec", "commit", "main", "a.txt"}, erun.RunOptions{Cwd: nested, Env: envVars, Stdin: "fix a only\n"})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an unrelated in-root change, got 0:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "b.txt") {
		t.Fatalf("expected the refusal to name b.txt, got:\n%s", result.Combined)
	}
}

func testExecCommitDryRunTracesNeutralizedColonPathspecMagic(t *testing.T) {
	// A clean tree isolates the "./" argv neutralization from the scope guard,
	// which would also refuse this path.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "commit", "main", ":/README.md", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "escape attempt\n"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/commit_dry_run_traces_neutralized_colon_pathspec_magic", normalize.Apply(result.Combined))
}

func testExecCommitScopedPathsPathspecMagicEscapeAttemptStaysRefused(t *testing.T) {
	// The scope guard alone refuses this path; the dry-run scenario above pins the
	// "./" argv neutralization.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	nested := filepath.Join(setup.Cwd, "nested")
	mustWriteFile(t, filepath.Join(nested, "a.txt"), "a\n")
	fixture.RunGit(t, setup.Cwd, "add", "nested/a.txt")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "seed nested")
	mustWriteFile(t, filepath.Join(setup.Cwd, "README.md"), "# test\ntop-level edit\n")
	envVars := append(setup.Env(), "ERUN_REPO_PATH="+nested)
	result := erun.Run(t, []string{"exec", "commit", "main", ":/README.md"}, erun.RunOptions{Cwd: nested, Env: envVars, Stdin: "escape attempt\n"})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a pathspec-magic escape attempt, got 0:\n%s", result.Combined)
	}
	if status := captureGit(t, setup.Cwd, "status", "--porcelain", "--", "README.md"); strings.TrimSpace(status) == "" {
		t.Fatalf("expected README.md's change to remain uncommitted after the escape attempt was refused")
	}
	if log := captureGit(t, setup.Cwd, "log", "--oneline"); strings.Count(strings.TrimSpace(log), "\n") != 1 {
		t.Fatalf("expected only the seed commit, got log: %q", log)
	}
}
