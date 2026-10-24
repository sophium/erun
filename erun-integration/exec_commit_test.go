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
	// The motivating case: the caller wrote values.yaml and asks to commit
	// only that, but the tree also carries unrelated.txt from some other
	// writer. Before scoping existed, `git add -A` swept both into the
	// commit; now the unrelated change must never land, and the whole
	// commit is refused, loudly, rather than silently dropping it.
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
	// Locks the actual `git add` argv for a scoped commit: each declared
	// path is prefixed with "./" so a value that happens to start with
	// ":" is never reinterpreted as git pathspec magic.
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
	// An empty Paths list is the documented "commit everything" default,
	// but a list of only blank strings is not empty — it is a scope that
	// resolves to nothing, almost certainly a caller bug (an unfilled
	// template, a split on empty input), and must be refused rather than
	// silently falling back to the unscoped commit-everything behavior.
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
	// A scope naming a directory must stage everything under it; before
	// this fix the guard compared git's per-file changed list literally
	// against the declared directory, so every file inside it looked
	// "outside declared paths" and the commit was refused every time.
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
	// Git C-quotes non-ASCII paths in its default (non -z) output; the
	// scope guard must compare against the real filename, not the quoted
	// form, or it refuses a file that is genuinely inside the declared
	// scope and reports Files values that are not valid paths.
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
	// Before this fix, git's stderr was routed to io.Discard, so a
	// failure reached the caller as a bare "exit status 1" with no cause.
	// A scoped path that matches nothing makes `git add` fail with an
	// informative message; that message must survive to the caller.
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
	// Same defect, the other RunGit call site: a rejecting pre-commit
	// hook's stderr must reach the caller, not just a bare exit status.
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
	// A pre-commit hook can switch HEAD before the commit lands; the
	// result must report where the commit actually landed, not echo back
	// the declared branch that was only true before the hook ran.
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
	// The scope guard's changed-files read must reason in root's own
	// basis. Before this fix it read repo-wide, top-level-relative
	// paths while comparing them against root-relative declared paths,
	// so any real change made every scoped commit fail regardless of
	// where it actually was. Scoping the reads to root (via --relative)
	// bounds the guard to the same tree the write/commit operations are
	// otherwise confined to, so a change entirely outside root is simply
	// not this operation's concern.
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
	// Complement to the previous scenario: a change that genuinely is
	// inside root's own subtree, but outside the declared scope, must
	// still be caught after the basis fix — the guard now compares like
	// with like, not "always refuse" or "never refuse".
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
	// ":/README.md" is git pathspec magic for "README.md relative to the
	// top of the working tree", regardless of what the filesystem-level
	// containment check makes of the literal string. A clean tree keeps
	// this isolated from the separate scope-guard fix above (which would
	// otherwise also refuse this exact declared path, for an unrelated
	// reason, and mask whether the argv itself was neutralized): the
	// traced `git add` argv is what would actually reach git, and must
	// show the "./" prefix that keeps the leading ":" from being
	// reinterpreted as pathspec magic.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "commit", "main", ":/README.md", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "escape attempt\n"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/commit_dry_run_traces_neutralized_colon_pathspec_magic", normalize.Apply(result.Combined))
}

func testExecCommitScopedPathsPathspecMagicEscapeAttemptStaysRefused(t *testing.T) {
	// End-to-end companion to the dry-run isolation test above: with a
	// runtime repo root that is not the git top-level and a real dirty
	// file at the top level a magic pathspec could otherwise reach, the
	// escape attempt is refused and nothing lands. Note this scenario
	// alone does not isolate the "./" neutralization from the scope-guard
	// fix above — both defects live in the same nested-root shape, and
	// either fix alone already refuses this specific declared path — so
	// the dry-run test is what actually pins the argv-level fix.
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
