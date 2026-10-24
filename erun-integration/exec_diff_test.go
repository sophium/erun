package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testExecDiffDryRunTracesGitDiff(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "diff", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "exec/diff_dry_run_traces_git_diff", normalize.Apply(result.Combined))
}

func testExecDiffDryRunErrorsOutsideGitProject(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"exec", "diff", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit outside a git project, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/diff_dry_run_errors_outside_git_project", normalize.Apply(result.Combined))
}

func testExecDiffJsonEmitsStructuredResult(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	mustWriteFile(t, filepath.Join(setup.Cwd, "README.md"), "# test\nadded line\n")
	result := erun.Run(t, []string{"exec", "diff", "--json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	var parsed lifecycle.DiffResult
	if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
		t.Fatalf("decode --json output: %v\n%s", err, result.Stdout)
	}
	if parsed.Summary.FileCount != 1 {
		t.Errorf("expected FileCount=1, got %d", parsed.Summary.FileCount)
	}
	if parsed.Summary.Additions == 0 {
		t.Errorf("expected non-zero additions, got %d", parsed.Summary.Additions)
	}
	if !parsed.IncludesWorktree {
		t.Errorf("expected IncludesWorktree=true")
	}
	if len(parsed.Files) != 1 || parsed.Files[0].Path != "README.md" {
		t.Errorf("expected single file README.md, got %+v", parsed.Files)
	}
	if len(parsed.Tree) == 0 {
		t.Errorf("expected non-empty Tree, got nil")
	}
	if parsed.RawDiff == "" {
		t.Errorf("expected non-empty RawDiff")
	}
}

func testExecDiffIncludesUntrackedFiles(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	mustWriteFile(t, filepath.Join(setup.Cwd, "new-file.txt"), "untracked content\n")
	result := erun.Run(t, []string{"exec", "diff", "--json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	var parsed lifecycle.DiffResult
	if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
		t.Fatalf("decode --json output: %v\n%s", err, result.Stdout)
	}
	found := false
	for _, f := range parsed.Files {
		if f.Path == "new-file.txt" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected new-file.txt in parsed files, got %+v", parsed.Files)
	}
}

func testExecDiffFilesMatchTreeOrder(t *testing.T) {
	// Files must follow the tree's directory-grouped leaf order, not git's raw order.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	if err := os.MkdirAll(filepath.Join(setup.Cwd, "dir"), 0o755); err != nil {
		t.Fatalf("mkdir dir: %v", err)
	}
	mustWriteFile(t, filepath.Join(setup.Cwd, "dir", "a.txt"), "a\n")
	mustWriteFile(t, filepath.Join(setup.Cwd, "root.txt"), "root\n")
	fixture.RunGit(t, setup.Cwd, "add", "dir/a.txt", "root.txt")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "seed tracked files")
	mustWriteFile(t, filepath.Join(setup.Cwd, "dir", "a.txt"), "a\nedit\n")
	mustWriteFile(t, filepath.Join(setup.Cwd, "root.txt"), "root\nedit\n")
	mustWriteFile(t, filepath.Join(setup.Cwd, "dir", "b.txt"), "b untracked\n")
	result := erun.Run(t, []string{"exec", "diff", "--json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	var parsed lifecycle.DiffResult
	if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
		t.Fatalf("decode --json output: %v\n%s", err, result.Stdout)
	}
	filePaths := make([]string, 0, len(parsed.Files))
	for _, f := range parsed.Files {
		filePaths = append(filePaths, f.Path)
	}
	treeLeafPaths := make([]string, 0, len(parsed.Tree))
	for _, node := range parsed.Tree {
		if node.Type == "file" {
			treeLeafPaths = append(treeLeafPaths, node.Path)
		}
	}
	if strings.Join(filePaths, ",") != strings.Join(treeLeafPaths, ",") {
		t.Errorf("Files order %v does not match tree leaf order %v", filePaths, treeLeafPaths)
	}
	if strings.Join(filePaths, ",") != "dir/a.txt,dir/b.txt,root.txt" {
		t.Errorf("expected Files order [dir/a.txt dir/b.txt root.txt], got %v", filePaths)
	}
}

func testExecDiffScopeAllTracesReviewBase(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	mustWriteFile(t, filepath.Join(setup.Cwd, "feature.txt"), "feature\n")
	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "feature")
	fixture.RunGit(t, setup.Cwd, "add", "feature.txt")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "feature commit")
	mustWriteFile(t, filepath.Join(setup.Cwd, "feature.txt"), "feature\nworktree edit\n")
	result := erun.Run(t, []string{"exec", "diff", "--json", "--scope=all"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	var parsed lifecycle.DiffResult
	if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
		t.Fatalf("decode --json output: %v\n%s", err, result.Stdout)
	}
	if parsed.Scope != "all" {
		t.Errorf("expected Scope=all, got %q", parsed.Scope)
	}
	if parsed.ReviewBase.Commit == "" {
		t.Errorf("expected ReviewBase.Commit to be populated, got %+v", parsed.ReviewBase)
	}
	if len(parsed.ReviewCommits) == 0 {
		t.Errorf("expected ReviewCommits to include the feature commit, got nil")
	}
	if parsed.Summary.FileCount == 0 {
		t.Errorf("expected non-zero FileCount across base..HEAD + worktree")
	}
}

func testExecDiffScopeCurrentCommittedChangeStillReportsReviewCommits(t *testing.T) {
	// ReviewCommits comes from base..HEAD regardless of scope, so it stays populated on a clean worktree.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "feature")
	mustWriteFile(t, filepath.Join(setup.Cwd, "feature.txt"), "feature\n")
	fixture.RunGit(t, setup.Cwd, "add", "feature.txt")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "feature commit")
	result := erun.Run(t, []string{"exec", "diff", "--json", "--scope=current"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	var parsed lifecycle.DiffResult
	if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
		t.Fatalf("decode --json output: %v\n%s", err, result.Stdout)
	}
	if parsed.Scope != "current" {
		t.Errorf("expected Scope=current, got %q", parsed.Scope)
	}
	if len(parsed.Files) != 0 {
		t.Errorf("expected no files under scope=current on a clean worktree, got %+v", parsed.Files)
	}
	if len(parsed.ReviewCommits) == 0 {
		t.Errorf("expected ReviewCommits to still report the unpushed feature commit even though scope=current's own diff is empty")
	}
}

func testExecDiffScopeCurrentDefaultIncludesStagedChanges(t *testing.T) {
	// scope=current diffs against HEAD so staged edits show up.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	mustWriteFile(t, filepath.Join(setup.Cwd, "README.md"), "# test\nstaged edit\n")
	fixture.RunGit(t, setup.Cwd, "add", "README.md")
	result := erun.Run(t, []string{"exec", "diff", "--json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	var parsed lifecycle.DiffResult
	if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
		t.Fatalf("decode --json output: %v\n%s", err, result.Stdout)
	}
	if len(parsed.Files) != 1 || parsed.Files[0].Path != "README.md" {
		t.Errorf("expected staged README.md to appear with no --scope flag, got %+v", parsed.Files)
	}
}

func testExecDiffScopeCurrentExplicitMatchesDefaultForStagedChanges(t *testing.T) {
	// The implicit default and explicit --scope=current take different code paths.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	mustWriteFile(t, filepath.Join(setup.Cwd, "README.md"), "# test\nstaged edit\n")
	fixture.RunGit(t, setup.Cwd, "add", "README.md")
	result := erun.Run(t, []string{"exec", "diff", "--json", "--scope=current"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	var parsed lifecycle.DiffResult
	if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
		t.Fatalf("decode --json output: %v\n%s", err, result.Stdout)
	}
	if len(parsed.Files) != 1 || parsed.Files[0].Path != "README.md" {
		t.Errorf("expected staged README.md under explicit --scope=current, got %+v", parsed.Files)
	}
}

func testExecDiffScopeCurrentNoCommitsYetStillDiffsStagedChanges(t *testing.T) {
	// With no HEAD, scope=current falls back to the index-only diff.
	setup := env.New(t)
	fixture.RunGit(t, setup.Cwd, "init", "-q", "-b", "main")
	fixture.RunGit(t, setup.Cwd, "config", "user.email", "test@example")
	fixture.RunGit(t, setup.Cwd, "config", "user.name", "Test")
	mustWriteFile(t, filepath.Join(setup.Cwd, "new.txt"), "new\n")
	fixture.RunGit(t, setup.Cwd, "add", "new.txt")
	result := erun.Run(t, []string{"exec", "diff", "--json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	var parsed lifecycle.DiffResult
	if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
		t.Fatalf("decode --json output: %v\n%s", err, result.Stdout)
	}
	if len(parsed.Files) != 1 || parsed.Files[0].Path != "new.txt" {
		t.Errorf("expected staged new.txt on a repo with no commits yet, got %+v", parsed.Files)
	}
}

func testExecDiffScopeCommitUsesSelectedCommitParent(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	mustWriteFile(t, filepath.Join(setup.Cwd, "second.txt"), "second\n")
	fixture.RunGit(t, setup.Cwd, "add", "second.txt")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "second commit")
	hash := strings.TrimSpace(captureGit(t, setup.Cwd, "rev-parse", "HEAD"))
	result := erun.Run(t, []string{"exec", "diff", "--json", "--scope=commit", "--selected-commit=" + hash}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	var parsed lifecycle.DiffResult
	if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
		t.Fatalf("decode --json output: %v\n%s", err, result.Stdout)
	}
	if parsed.Scope != "commit" {
		t.Errorf("expected Scope=commit, got %q", parsed.Scope)
	}
	if parsed.SelectedCommit != hash {
		t.Errorf("expected SelectedCommit=%s, got %q", hash, parsed.SelectedCommit)
	}
	if parsed.Summary.FileCount == 0 {
		t.Errorf("expected non-zero FileCount across selected commit, got 0")
	}
}

func testExecDiffParsesDeletedAndBinaryFiles(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	binPath := filepath.Join(setup.Cwd, "img.bin")
	if err := os.WriteFile(binPath, []byte{0x00, 0x01, 0x02, 0x03}, 0o644); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	fixture.RunGit(t, setup.Cwd, "add", "img.bin")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add binary")
	if err := os.WriteFile(binPath, []byte{0x00, 0xff, 0xfe, 0xfd}, 0o644); err != nil {
		t.Fatalf("rewrite binary: %v", err)
	}
	if err := os.Remove(filepath.Join(setup.Cwd, "README.md")); err != nil {
		t.Fatalf("remove README: %v", err)
	}
	result := erun.Run(t, []string{"exec", "diff", "--json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	var parsed lifecycle.DiffResult
	if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
		t.Fatalf("decode --json output: %v\n%s", err, result.Stdout)
	}
	assertDeletedAndBinaryDiffFiles(t, parsed.Files)
}

func assertDeletedAndBinaryDiffFiles(t *testing.T, files []lifecycle.DiffFile) {
	t.Helper()
	statuses := map[string]lifecycle.DiffFile{}
	for _, f := range files {
		statuses[f.Path] = f
	}
	if f, ok := statuses["README.md"]; !ok || f.Status != "deleted" {
		t.Errorf("expected README.md status=deleted, got %+v", files)
	}
	if f, ok := statuses["img.bin"]; !ok || !f.Binary {
		t.Errorf("expected img.bin binary=true, got %+v", files)
	}
}

func testExecDiffScopeAllResolvesOriginHeadAndRenames(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.RunGit(t, setup.Cwd, "update-ref", "refs/remotes/origin/main", "main")
	fixture.RunGit(t, setup.Cwd, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "feature")
	fixture.RunGit(t, setup.Cwd, "mv", "README.md", "RENAMED.md")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "rename readme")
	result := erun.Run(t, []string{"exec", "diff", "--json", "--scope=all"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	var parsed lifecycle.DiffResult
	if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
		t.Fatalf("decode --json output: %v\n%s", err, result.Stdout)
	}
	if parsed.ReviewBase.Branch != "origin/main" {
		t.Errorf("expected ReviewBase.Branch=origin/main via symbolic-ref, got %q", parsed.ReviewBase.Branch)
	}
	if len(parsed.Files) != 1 {
		t.Fatalf("expected single renamed file, got %+v", parsed.Files)
	}
	file := parsed.Files[0]
	if file.Status != "renamed" || file.OldPath != "README.md" || file.NewPath != "RENAMED.md" {
		t.Errorf("expected renamed README.md->RENAMED.md, got %+v", file)
	}
}

func testExecDiffRawOutputIncludesDeletions(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	mustWriteFile(t, filepath.Join(setup.Cwd, "README.md"), "rewritten\n")
	result := erun.Run(t, []string{"exec", "diff"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/diff_raw_output_includes_deletions", normalize.Apply(result.Combined))
}

func testExecDiffSelectedCommitWithoutScopeErrors(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "diff", "--selected-commit=abc1234"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/diff_selected_commit_without_scope_errors", normalize.Apply(result.Combined))
}

func testExecDiffSelectedCommitOptionShapedRefused(t *testing.T) {
	// An option-shaped selectedCommit must be refused before git runs; the stub git fails loudly to prove it.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "git", fixture.StubBinarySpec{Stderr: "stub-git-must-not-run", ExitCode: 1})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "git")...)
	result := erun.Run(t, []string{"exec", "diff", "--scope=commit", "--selected-commit=--not-a-revision"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "stub-git-must-not-run") {
		t.Fatalf("git ran even though the option-shaped selectedCommit should have been refused first: %s", result.Combined)
	}
	golden.Equal(t, "exec/diff_selected_commit_option_shaped_refused", normalize.Apply(result.Combined))
}

func testExecDiffSelectedCommitMalformedRevisionErrors(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "diff", "--scope=commit", "--selected-commit=not-a-real-commit"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/diff_selected_commit_malformed_revision_errors", normalize.Apply(result.Combined))
}

func testExecDiffScopeCommitResolvesAbbreviatedCommit(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	mustWriteFile(t, filepath.Join(setup.Cwd, "second.txt"), "second\n")
	fixture.RunGit(t, setup.Cwd, "add", "second.txt")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "second commit")
	fullHash := strings.TrimSpace(captureGit(t, setup.Cwd, "rev-parse", "HEAD"))
	shortHash := strings.TrimSpace(captureGit(t, setup.Cwd, "rev-parse", "--short", "HEAD"))
	result := erun.Run(t, []string{"exec", "diff", "--json", "--scope=commit", "--selected-commit=" + shortHash}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	var parsed lifecycle.DiffResult
	if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
		t.Fatalf("decode --json output: %v\n%s", err, result.Stdout)
	}
	if parsed.SelectedCommit != fullHash {
		t.Errorf("expected resolved SelectedCommit=%s, got %q", fullHash, parsed.SelectedCommit)
	}
	if parsed.Summary.FileCount == 0 {
		t.Errorf("expected non-zero FileCount across selected commit, got 0")
	}
}
