package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	common "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func mustWriteFile(t testing.TB, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustReadFile(t testing.TB, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func captureGit(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := osexec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

// execDangerousContent carries the constructs a shell would reinterpret —
// backticks, command substitution, embedded quotes, a trailing newline — so a
// round trip through `exec write` / `exec commit` demonstrates the property
// that justifies bypassing raw for these two operations: content and message
// travel as data and are never shell-parsed.
const execDangerousContent = "line one\n`echo pwned` $(echo pwned) \"quoted\" 'quoted'\ntrailing\n\n"

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

	t.Run("raw_help", func(t *testing.T) {
		// raw disables flag parsing, so --help must be intercepted explicitly
		// and render help rather than being run as a binary named "--help".
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "raw", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/raw_help", normalize.Apply(result.Combined))
	})

	t.Run("raw_dry_run_traces_inside_project", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "raw", "echo", "hello", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "exec/raw_dry_run_traces_inside_project", normalize.Apply(result.Combined))
	})

	t.Run("raw_dry_run_redacts_sensitive_args", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "raw", "--dry-run", "curl", "https://example", "--token", "secret-value", "--password=hidden", "ok"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "exec/raw_dry_run_redacts_sensitive_args", normalize.Apply(result.Combined))
	})

	t.Run("raw_dry_run_double_dash_passes_flags_through", func(t *testing.T) {
		// raw disables flag parsing, yet erun's own --dry-run=true before --
		// is still consumed while everything after -- passes through verbatim,
		// including the wrapped command's own --dry-run and --help.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "raw", "--dry-run=true", "--", "echo", "--dry-run", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/raw_dry_run_double_dash_passes_flags_through", normalize.Apply(result.Combined))
	})

	t.Run("diff_dry_run_traces_git_diff", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "diff", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "exec/diff_dry_run_traces_git_diff", normalize.Apply(result.Combined))
	})

	t.Run("diff_dry_run_errors_outside_git_project", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "diff", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit outside a git project, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/diff_dry_run_errors_outside_git_project", normalize.Apply(result.Combined))
	})

	t.Run("diff_json_emits_structured_result", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		mustWriteFile(t, filepath.Join(setup.Cwd, "README.md"), "# test\nadded line\n")
		result := erun.Run(t, []string{"exec", "diff", "--json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		var parsed common.DiffResult
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
	})

	t.Run("diff_includes_untracked_files", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		mustWriteFile(t, filepath.Join(setup.Cwd, "new-file.txt"), "untracked content\n")
		result := erun.Run(t, []string{"exec", "diff", "--json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		var parsed common.DiffResult
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
	})

	t.Run("diff_files_match_tree_order", func(t *testing.T) {
		// The diff panel renders Files while the changed-files list renders
		// Tree, so Files must come out in the tree's directory-grouped leaf
		// order, not git's raw order; the two diverge when an untracked file
		// (last in git order) sits in a directory whose first entry is a
		// tracked change.
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
		var parsed common.DiffResult
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
	})

	t.Run("diff_scope_all_traces_review_base", func(t *testing.T) {
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
		var parsed common.DiffResult
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
	})

	t.Run("diff_scope_current_committed_change_still_reports_review_commits", func(t *testing.T) {
		// Regression: the panel's default "current" scope only ever diffs
		// uncommitted local edits, so a clean worktree right after a commit
		// correctly shows an empty diff -- but the caller must still be able
		// to tell that apart from "nothing to review at all". ReviewCommits
		// is computed from base..HEAD regardless of scope, so it must stay
		// populated here even though scope=current's own diff is empty.
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
		var parsed common.DiffResult
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
	})

	t.Run("diff_scope_current_default_includes_staged_changes", func(t *testing.T) {
		// Bug: scope=current used to run a bare `git diff` (worktree vs
		// index), which misses a staged-but-uncommitted change entirely --
		// exactly the state right after `git add`. It must diff against HEAD
		// instead so staged edits show up alongside unstaged ones.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		mustWriteFile(t, filepath.Join(setup.Cwd, "README.md"), "# test\nstaged edit\n")
		fixture.RunGit(t, setup.Cwd, "add", "README.md")
		result := erun.Run(t, []string{"exec", "diff", "--json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		var parsed common.DiffResult
		if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
			t.Fatalf("decode --json output: %v\n%s", err, result.Stdout)
		}
		if len(parsed.Files) != 1 || parsed.Files[0].Path != "README.md" {
			t.Errorf("expected staged README.md to appear with no --scope flag, got %+v", parsed.Files)
		}
	})

	t.Run("diff_scope_current_explicit_matches_default_for_staged_changes", func(t *testing.T) {
		// The CLI's implicit default (no --scope flag at all, ResolveGitDiff)
		// and an explicit --scope=current (ResolveGitDiffWithOptions) are two
		// different code paths that must resolve staged changes the same way
		// rather than silently diverging.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		mustWriteFile(t, filepath.Join(setup.Cwd, "README.md"), "# test\nstaged edit\n")
		fixture.RunGit(t, setup.Cwd, "add", "README.md")
		result := erun.Run(t, []string{"exec", "diff", "--json", "--scope=current"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		var parsed common.DiffResult
		if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
			t.Fatalf("decode --json output: %v\n%s", err, result.Stdout)
		}
		if len(parsed.Files) != 1 || parsed.Files[0].Path != "README.md" {
			t.Errorf("expected staged README.md under explicit --scope=current, got %+v", parsed.Files)
		}
	})

	t.Run("diff_scope_current_no_commits_yet_still_diffs_staged_changes", func(t *testing.T) {
		// A repo with no commits at all has no HEAD to diff against;
		// scope=current must fall back to the index-only form rather than
		// erroring out on a missing HEAD.
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
		var parsed common.DiffResult
		if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
			t.Fatalf("decode --json output: %v\n%s", err, result.Stdout)
		}
		if len(parsed.Files) != 1 || parsed.Files[0].Path != "new.txt" {
			t.Errorf("expected staged new.txt on a repo with no commits yet, got %+v", parsed.Files)
		}
	})

	t.Run("diff_scope_commit_uses_selected_commit_parent", func(t *testing.T) {
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
		var parsed common.DiffResult
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
	})

	t.Run("diff_parses_deleted_and_binary_files", func(t *testing.T) {
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
		var parsed common.DiffResult
		if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
			t.Fatalf("decode --json output: %v\n%s", err, result.Stdout)
		}
		statuses := map[string]common.DiffFile{}
		for _, f := range parsed.Files {
			statuses[f.Path] = f
		}
		if f, ok := statuses["README.md"]; !ok || f.Status != "deleted" {
			t.Errorf("expected README.md status=deleted, got %+v", parsed.Files)
		}
		if f, ok := statuses["img.bin"]; !ok || !f.Binary {
			t.Errorf("expected img.bin binary=true, got %+v", parsed.Files)
		}
	})

	t.Run("diff_scope_all_resolves_origin_head_and_renames", func(t *testing.T) {
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
		var parsed common.DiffResult
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
	})

	t.Run("diff_raw_output_includes_deletions", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		mustWriteFile(t, filepath.Join(setup.Cwd, "README.md"), "rewritten\n")
		result := erun.Run(t, []string{"exec", "diff"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/diff_raw_output_includes_deletions", normalize.Apply(result.Combined))
	})

	t.Run("diff_selected_commit_without_scope_errors", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "diff", "--selected-commit=abc1234"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/diff_selected_commit_without_scope_errors", normalize.Apply(result.Combined))
	})

	t.Run("diff_selected_commit_option_shaped_refused", func(t *testing.T) {
		// exec_diff is a read-scoped capability: git reads a value starting
		// with "-" as an option rather than a revision, so a selectedCommit in
		// that shape must be refused before it ever reaches git's argv. Stub
		// git to fail loudly so the assertion proves the refusal happens
		// before any git invocation, not just that the command errors.
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
	})

	t.Run("diff_selected_commit_malformed_revision_errors", func(t *testing.T) {
		// A revision that is not option-shaped but does not resolve to any
		// commit (typo, unknown ref) must fail with a clear message naming the
		// value, not git's raw "fatal: ..." text.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "diff", "--scope=commit", "--selected-commit=not-a-real-commit"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/diff_selected_commit_malformed_revision_errors", normalize.Apply(result.Combined))
	})

	t.Run("diff_scope_commit_resolves_abbreviated_commit", func(t *testing.T) {
		// The fix resolves selectedCommit to a full sha via `git rev-parse
		// --verify` before it reaches the diff argv, so an abbreviated hash
		// must still produce the same diff and echo the resolved full sha.
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
		var parsed common.DiffResult
		if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
			t.Fatalf("decode --json output: %v\n%s", err, result.Stdout)
		}
		if parsed.SelectedCommit != fullHash {
			t.Errorf("expected resolved SelectedCommit=%s, got %q", fullHash, parsed.SelectedCommit)
		}
		if parsed.Summary.FileCount == 0 {
			t.Errorf("expected non-zero FileCount across selected commit, got 0")
		}
	})

	t.Run("dry_run_with_time_flag_prints_elapsed_on_error", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "diff", "--dry-run", "--time"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit (no git project), got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/dry_run_with_time_flag_prints_elapsed_on_error", normalize.Apply(result.Combined))
	})

	t.Run("write_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "write", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/write_help", normalize.Apply(result.Combined))
	})

	t.Run("write_dry_run_traces_resolved_path", func(t *testing.T) {
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
	})

	t.Run("write_dry_run_errors_outside_git_project", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "write", "foo.txt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "x"})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit outside a git project, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/write_dry_run_errors_outside_git_project", normalize.Apply(result.Combined))
	})

	t.Run("write_refuses_path_outside_project_root", func(t *testing.T) {
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
	})

	t.Run("write_refuses_symlink_leaf_pointing_outside_tree", func(t *testing.T) {
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
	})

	t.Run("write_refuses_write_through_symlinked_directory_component", func(t *testing.T) {
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
	})

	t.Run("write_root_itself_symlink_still_writes_inside_real_tree", func(t *testing.T) {
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
	})

	t.Run("write_refuses_absolute_path_outside_root", func(t *testing.T) {
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
	})

	t.Run("write_refuses_path_outside_root_after_dotdot_normalisation", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "write", "sub/../../escape.txt"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "x"})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a path escaping via nested ../.., got 0:\n%s", result.Combined)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(setup.Cwd), "escape.txt")); !os.IsNotExist(err) {
			t.Fatalf("expected no file to land outside the project root")
		}
	})

	t.Run("write_content_round_trips_byte_identical_with_dangerous_shell_constructs", func(t *testing.T) {
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
	})

	t.Run("commit_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "commit", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/commit_help", normalize.Apply(result.Combined))
	})

	t.Run("commit_dry_run_verifies_branch_and_traces", func(t *testing.T) {
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
	})

	t.Run("commit_dry_run_refuses_branch_mismatch", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "commit", "not-main", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "message\n"})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a branch mismatch under --dry-run, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/commit_dry_run_refuses_branch_mismatch", normalize.Apply(result.Combined))
	})

	t.Run("commit_refuses_branch_mismatch", func(t *testing.T) {
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
	})

	t.Run("commit_commits_dangerous_message_and_reports_result", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		mustWriteFile(t, filepath.Join(setup.Cwd, "values.yaml"), "typo: fixed\n")
		result := erun.Run(t, []string{"exec", "commit", "main", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: execDangerousContent})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		var parsed common.CommitWorkingTreeResult
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
	})

	t.Run("commit_refuses_when_nothing_to_commit", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "commit", "main"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "message\n"})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for nothing to commit, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/commit_refuses_when_nothing_to_commit", normalize.Apply(result.Combined))
	})

	t.Run("commit_scoped_paths_refuses_unrelated_dirty_file", func(t *testing.T) {
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
	})

	t.Run("commit_scoped_paths_commits_only_the_declared_file", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		mustWriteFile(t, filepath.Join(setup.Cwd, "values.yaml"), "typo: fixed\n")
		result := erun.Run(t, []string{"exec", "commit", "main", "values.yaml", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "fix the values typo\n"})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		var parsed common.CommitWorkingTreeResult
		if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
			t.Fatalf("decode --output json: %v\n%s", err, result.Stdout)
		}
		if len(parsed.Files) != 1 || parsed.Files[0] != "values.yaml" {
			t.Fatalf("expected committed files [values.yaml], got %+v", parsed.Files)
		}
	})

	t.Run("commit_dry_run_scoped_paths_traces_neutralized_add_args", func(t *testing.T) {
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
	})

	t.Run("commit_dry_run_names_files_that_would_be_committed", func(t *testing.T) {
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
	})

	t.Run("commit_dry_run_scoped_paths_refuses_unrelated_dirty_file", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		mustWriteFile(t, filepath.Join(setup.Cwd, "values.yaml"), "typo: fixed\n")
		mustWriteFile(t, filepath.Join(setup.Cwd, "unrelated.txt"), "someone else's in-flight work\n")
		result := erun.Run(t, []string{"exec", "commit", "main", "values.yaml", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "fix the values typo\n"})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a scoped dry-run with unrelated changes present, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/commit_dry_run_scoped_paths_refuses_unrelated_dirty_file", normalize.Apply(result.Combined))
	})

	t.Run("commit_scoped_paths_rejects_blank_path_entries", func(t *testing.T) {
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
	})

	t.Run("commit_scoped_paths_accepts_directory_scope", func(t *testing.T) {
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
		var parsed common.CommitWorkingTreeResult
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
	})

	t.Run("commit_scoped_paths_accepts_non_ascii_filename", func(t *testing.T) {
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
		var parsed common.CommitWorkingTreeResult
		if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
			t.Fatalf("decode --output json: %v\n%s", err, result.Stdout)
		}
		if len(parsed.Files) != 1 || parsed.Files[0] != name {
			t.Fatalf("expected committed files [%s], got %+v", name, parsed.Files)
		}
	})

	t.Run("commit_surfaces_git_add_failure_output", func(t *testing.T) {
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
	})

	t.Run("commit_surfaces_git_commit_failure_output", func(t *testing.T) {
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
	})

	t.Run("commit_reports_the_branch_the_commit_actually_landed_on", func(t *testing.T) {
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
		var parsed common.CommitWorkingTreeResult
		if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
			t.Fatalf("decode --output json: %v\n%s", err, result.Stdout)
		}
		if parsed.Branch != "diverted" {
			t.Fatalf("expected the result to report the branch the commit actually landed on (diverted), got %q", parsed.Branch)
		}
	})

	t.Run("commit_root_not_git_toplevel_ignores_changes_outside_root", func(t *testing.T) {
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
		var parsed common.CommitWorkingTreeResult
		if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
			t.Fatalf("decode --output json: %v\n%s", err, result.Stdout)
		}
		if len(parsed.Files) != 1 || parsed.Files[0] != "a.txt" {
			t.Fatalf("expected committed files [a.txt], got %+v", parsed.Files)
		}
		if status := captureGit(t, setup.Cwd, "status", "--porcelain", "--", "README.md"); strings.TrimSpace(status) == "" {
			t.Fatalf("expected README.md's unrelated change to remain uncommitted")
		}
	})

	t.Run("commit_root_not_git_toplevel_still_refuses_unrelated_change_inside_root", func(t *testing.T) {
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
	})

	t.Run("commit_dry_run_traces_neutralized_colon_pathspec_magic", func(t *testing.T) {
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
	})

	t.Run("commit_scoped_paths_pathspec_magic_escape_attempt_stays_refused", func(t *testing.T) {
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
	})

	t.Run("push_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "push", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/push_help", normalize.Apply(result.Combined))
	})

	t.Run("push_dry_run_verifies_branch_and_traces", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "push", "main", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/push_dry_run_verifies_branch_and_traces", normalize.Apply(result.Combined))
	})

	t.Run("push_dry_run_refuses_branch_mismatch", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "push", "not-main", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a branch mismatch under --dry-run, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/push_dry_run_refuses_branch_mismatch", normalize.Apply(result.Combined))
	})

	t.Run("push_refuses_branch_mismatch", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "push", "not-main"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a branch mismatch, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/push_refuses_branch_mismatch", normalize.Apply(result.Combined))
	})

	t.Run("push_real_run_lands_the_branch_on_the_remote", func(t *testing.T) {
		// A real bare remote, not a stubbed git call, so "the branch actually
		// landed on the remote" is an observable fact — the whole point of a
		// typed push primitive over `raw` running `git push` (#1199).
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		remote := seedBareOrigin(t, setup)
		fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "feature")
		mustWriteFile(t, filepath.Join(setup.Cwd, "feature.txt"), "feature\n")
		fixture.RunGit(t, setup.Cwd, "add", "feature.txt")
		fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "feature commit")
		result := erun.Run(t, []string{"exec", "push", "feature", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		var parsed common.PushWorkingTreeBranchResult
		if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
			t.Fatalf("decode --output json: %v\n%s", err, result.Stdout)
		}
		if parsed.Branch != "feature" || parsed.Remote != "origin" || parsed.Commit == "" {
			t.Fatalf("unexpected result: %+v", parsed)
		}
		branches := captureGit(t, remote, "branch", "--list", "feature")
		if !strings.Contains(branches, "feature") {
			t.Fatalf("expected origin to carry the pushed feature branch, got: %q", branches)
		}
	})

	t.Run("push_dry_run_must_not_reach_the_network", func(t *testing.T) {
		// A dry run against a branch with no remote at all must still succeed:
		// the whole point of --dry-run is that it never actually pushes.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "push", "main", "--remote", "nonexistent", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "nonexistent") {
			t.Fatalf("expected the trace to name the declared remote, got:\n%s", result.Combined)
		}
	})

	t.Run("merge_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "merge", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/merge_help", normalize.Apply(result.Combined))
	})

	t.Run("merge_dry_run_must_not_reach_the_network", func(t *testing.T) {
		// A dry run against a branch with no real remote must still succeed:
		// --dry-run never fetches or merges, mirroring push's own network-free
		// dry-run contract.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "merge", "main", "--remote", "nonexistent", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/merge_dry_run_must_not_reach_the_network", normalize.Apply(result.Combined))
	})

	t.Run("merge_real_run_merges_the_target_branch", func(t *testing.T) {
		// A real bare remote and a real divergent target branch, so "the merge
		// actually landed" is an observable fact rather than a stubbed git call.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		seedBareOrigin(t, setup)

		fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "feature")
		mustWriteFile(t, filepath.Join(setup.Cwd, "feature.txt"), "feature\n")
		fixture.RunGit(t, setup.Cwd, "add", "feature.txt")
		fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "feature commit")

		fixture.RunGit(t, setup.Cwd, "checkout", "-q", "main")
		mustWriteFile(t, filepath.Join(setup.Cwd, "main.txt"), "main\n")
		fixture.RunGit(t, setup.Cwd, "add", "main.txt")
		fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "main commit")
		fixture.RunGit(t, setup.Cwd, "push", "-q", "origin", "main")
		fixture.RunGit(t, setup.Cwd, "checkout", "-q", "feature")

		result := erun.Run(t, []string{"exec", "merge", "main", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		var parsed common.MergeWorkingTreeBranchResult
		if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
			t.Fatalf("decode --output json: %v\n%s", err, result.Stdout)
		}
		if parsed.Branch != "feature" || parsed.TargetBranch != "main" || parsed.Remote != "origin" || parsed.Commit == "" {
			t.Fatalf("unexpected result: %+v", parsed)
		}
		if _, err := os.Stat(filepath.Join(setup.Cwd, "main.txt")); err != nil {
			t.Fatalf("expected main.txt to be merged into the feature worktree: %v", err)
		}
	})

	t.Run("merge_conflict_real_run_leaves_the_worktree_mid_merge", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		seedBareOrigin(t, setup)

		fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "feature")
		mustWriteFile(t, filepath.Join(setup.Cwd, "README.md"), "feature change\n")
		fixture.RunGit(t, setup.Cwd, "add", "README.md")
		fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "feature edits readme")

		fixture.RunGit(t, setup.Cwd, "checkout", "-q", "main")
		mustWriteFile(t, filepath.Join(setup.Cwd, "README.md"), "main change\n")
		fixture.RunGit(t, setup.Cwd, "add", "README.md")
		fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "main edits readme")
		fixture.RunGit(t, setup.Cwd, "push", "-q", "origin", "main")
		fixture.RunGit(t, setup.Cwd, "checkout", "-q", "feature")

		result := erun.Run(t, []string{"exec", "merge", "main"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a conflicted merge, got 0:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "README.md") || !strings.Contains(result.Combined, "conflicted") {
			t.Fatalf("expected the conflicted file named in the error, got:\n%s", result.Combined)
		}
		status := captureGit(t, setup.Cwd, "status", "--porcelain")
		if !strings.Contains(status, "UU README.md") {
			t.Fatalf("expected the worktree to be left mid-merge, got status: %q", status)
		}
	})

	t.Run("gate_merge_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "gate-merge", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/gate_merge_help", normalize.Apply(result.Combined))
	})

	t.Run("gate_merge_refuses_missing_target", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "gate-merge", "feature/add-widget"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "Add widget\n"})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without --target, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/gate_merge_refuses_missing_target", normalize.Apply(result.Combined))
	})

	t.Run("gate_merge_dry_run_must_not_reach_the_network", func(t *testing.T) {
		// A dry run against a bogus remote must still succeed: --dry-run never
		// fetches, checks out, squash-merges, or commits, mirroring merge's own
		// network-free dry-run contract.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "gate-merge", "feature/add-widget", "--target", "main", "--remote", "nonexistent", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "Add widget\n"})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/gate_merge_dry_run_must_not_reach_the_network", normalize.Apply(result.Combined))
	})

	t.Run("gate_merge_dry_run_refuses_dirty_worktree", func(t *testing.T) {
		// The clean-worktree check runs even during --dry-run, the same
		// discipline exec commit/push apply to their own branch-mismatch check:
		// it is a read, not a mutation, so a dry run refuses exactly what a real
		// run would refuse. It checks only tracked-file changes (mirroring the
		// release flow's own worktree-clean check), so this dirties a tracked
		// file rather than adding an untracked one.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		mustWriteFile(t, filepath.Join(setup.Cwd, "README.md"), "uncommitted change\n")
		result := erun.Run(t, []string{"exec", "gate-merge", "feature/add-widget", "--target", "main", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "Add widget\n"})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit against a dirty worktree, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/gate_merge_dry_run_refuses_dirty_worktree", normalize.Apply(result.Combined))
	})

	t.Run("gate_merge_real_run_squash_merges_onto_target", func(t *testing.T) {
		// A real bare remote and a real divergent source branch, so "the squash
		// merge actually landed on target" is an observable fact rather than a
		// stubbed git call.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		seedBareOrigin(t, setup)

		fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "feature")
		mustWriteFile(t, filepath.Join(setup.Cwd, "feature.txt"), "feature\n")
		fixture.RunGit(t, setup.Cwd, "add", "feature.txt")
		fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "feature commit")
		fixture.RunGit(t, setup.Cwd, "push", "-u", "-q", "origin", "feature")
		fixture.RunGit(t, setup.Cwd, "checkout", "-q", "main")

		result := erun.Run(t, []string{"exec", "gate-merge", "feature", "--target", "main", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "Add widget\n"})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		var parsed common.GateMergeWorkingTreeResult
		if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
			t.Fatalf("decode --output json: %v\n%s", err, result.Stdout)
		}
		if parsed.TargetBranch != "main" || parsed.SourceBranch != "feature" || parsed.Remote != "origin" || parsed.Commit == "" || parsed.SourceCommit == "" {
			t.Fatalf("unexpected result: %+v", parsed)
		}
		if branch := strings.TrimSpace(captureGit(t, setup.Cwd, "rev-parse", "--abbrev-ref", "HEAD")); branch != "main" {
			t.Fatalf("expected the worktree to land on main, got %q", branch)
		}
		if _, err := os.Stat(filepath.Join(setup.Cwd, "feature.txt")); err != nil {
			t.Fatalf("expected feature.txt to be squash-merged onto main: %v", err)
		}
		parentCount := strings.TrimSpace(captureGit(t, setup.Cwd, "log", "-1", "--pretty=%P"))
		if strings.Contains(parentCount, " ") {
			t.Fatalf("expected a single-parent squash commit, got parents: %q", parentCount)
		}
		message := strings.TrimSpace(captureGit(t, setup.Cwd, "log", "-1", "--pretty=%s"))
		if message != "Add widget" {
			t.Fatalf("expected the squash commit message to be the given message, got %q", message)
		}
	})

	t.Run("gate_merge_conflict_real_run_leaves_the_worktree_mid_conflict", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		seedBareOrigin(t, setup)

		fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "feature")
		mustWriteFile(t, filepath.Join(setup.Cwd, "README.md"), "feature change\n")
		fixture.RunGit(t, setup.Cwd, "add", "README.md")
		fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "feature edits readme")
		fixture.RunGit(t, setup.Cwd, "push", "-u", "-q", "origin", "feature")

		fixture.RunGit(t, setup.Cwd, "checkout", "-q", "main")
		mustWriteFile(t, filepath.Join(setup.Cwd, "README.md"), "main change\n")
		fixture.RunGit(t, setup.Cwd, "add", "README.md")
		fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "main edits readme")
		fixture.RunGit(t, setup.Cwd, "push", "-q", "origin", "main")

		result := erun.Run(t, []string{"exec", "gate-merge", "feature", "--target", "main"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "Add widget\n"})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a conflicted squash merge, got 0:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "README.md") || !strings.Contains(result.Combined, "conflicted") {
			t.Fatalf("expected the conflicted file named in the error, got:\n%s", result.Combined)
		}
		status := captureGit(t, setup.Cwd, "status", "--porcelain")
		if !strings.Contains(status, "UU README.md") {
			t.Fatalf("expected the worktree to be left mid-conflict, got status: %q", status)
		}
	})

	t.Run("report_commit_status_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "report-commit-status", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/report_commit_status_help", normalize.Apply(result.Combined))
	})

	t.Run("report_commit_status_dry_run_default_context", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{
			"exec", "report-commit-status", "deadbeefcafe",
			"--state", "success",
			"--description", "gate build passed",
			"--remote-url", "https://github.com/sophium/erun.git",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/report_commit_status_dry_run_default_context", normalize.Apply(result.Combined))
	})

	t.Run("report_commit_status_dry_run_explicit_context_and_target_url", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{
			"exec", "report-commit-status", "deadbeefcafe",
			"--state", "failure",
			"--description", "erun build failed against the prospective merge",
			"--remote-url", "git@github.com:sophium/erun.git",
			"--context", "erun/custom-gate",
			"--target-url", "https://example.com/build/123",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/report_commit_status_dry_run_explicit_context_and_target_url", normalize.Apply(result.Combined))
	})

	t.Run("report_commit_status_dry_run_ssh_remote_url", func(t *testing.T) {
		// Exercises the ssh:// remote form, a distinct branch of
		// cutGitHubRemotePrefix from the https/git@ forms the other scenarios
		// already cover.
		setup := env.New(t)
		result := erun.Run(t, []string{
			"exec", "report-commit-status", "deadbeefcafe",
			"--state", "pending",
			"--description", "gate build running",
			"--remote-url", "ssh://git@github.com/sophium/erun.git",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/report_commit_status_dry_run_ssh_remote_url", normalize.Apply(result.Combined))
	})

	t.Run("report_commit_status_dry_run_empty_commit_traces_then_refuses", func(t *testing.T) {
		// COMMIT is a positional arg an empty string can still satisfy
		// cobra.ExactArgs(1), so the shared validation (and its trace) is what
		// actually catches this, not command-tree wiring.
		setup := env.New(t)
		result := erun.Run(t, []string{
			"exec", "report-commit-status", "",
			"--state", "success",
			"--description", "gate build passed",
			"--remote-url", "https://github.com/sophium/erun.git",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for an empty commit, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/report_commit_status_dry_run_empty_commit_traces_then_refuses", normalize.Apply(result.Combined))
	})

	t.Run("report_commit_status_dry_run_invalid_state_traces_then_refuses", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{
			"exec", "report-commit-status", "deadbeefcafe",
			"--state", "groovy",
			"--description", "gate build passed",
			"--remote-url", "https://github.com/sophium/erun.git",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for an invalid state, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/report_commit_status_dry_run_invalid_state_traces_then_refuses", normalize.Apply(result.Combined))
	})

	t.Run("report_commit_status_dry_run_missing_description_traces_then_refuses", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{
			"exec", "report-commit-status", "deadbeefcafe",
			"--state", "success",
			"--remote-url", "https://github.com/sophium/erun.git",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a missing description, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/report_commit_status_dry_run_missing_description_traces_then_refuses", normalize.Apply(result.Combined))
	})

	t.Run("report_commit_status_dry_run_malformed_remote_url_traces_then_refuses", func(t *testing.T) {
		// A non-github.com remote reaches parseGitHubOwnerRepo's rejection
		// branch, distinct from the missing-remote-url case below.
		setup := env.New(t)
		result := erun.Run(t, []string{
			"exec", "report-commit-status", "deadbeefcafe",
			"--state", "success",
			"--description", "gate build passed",
			"--remote-url", "https://gitlab.com/sophium/erun.git",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a non-github remote, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/report_commit_status_dry_run_malformed_remote_url_traces_then_refuses", normalize.Apply(result.Combined))
	})

	t.Run("report_commit_status_dry_run_missing_remote_url_traces_then_refuses", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{
			"exec", "report-commit-status", "deadbeefcafe",
			"--state", "success",
			"--description", "gate build passed",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a missing remote-url, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/report_commit_status_dry_run_missing_remote_url_traces_then_refuses", normalize.Apply(result.Combined))
	})

	t.Run("report_commit_status_real_run_fails_cleanly_without_a_token", func(t *testing.T) {
		// No --dry-run, no gh on the scrubbed PATH, and no GITHUB_TOKEN/GH_TOKEN
		// in the environment: the real-run token resolution must refuse before
		// ever reaching the network, naming the fix rather than a raw HTTP error.
		setup := env.New(t)
		result := erun.Run(t, []string{
			"exec", "report-commit-status", "deadbeefcafe",
			"--state", "success",
			"--description", "gate build passed",
			"--remote-url", "https://github.com/sophium/erun.git",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit with no token available, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/report_commit_status_real_run_fails_cleanly_without_a_token", normalize.Apply(result.Combined))
	})

	t.Run("close_pr_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "close-pr", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/close_pr_help", normalize.Apply(result.Combined))
	})

	t.Run("close_pr_dry_run_traces_lookup", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{
			"exec", "close-pr", "feature/add-widget",
			"--target", "main",
			"--remote-url", "https://github.com/sophium/erun.git",
			"--gated-commit", "sourcesha0000000000000000000000000000000",
			"--landing-commit", "landedsha0000000000000000000000000000000",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/close_pr_dry_run_traces_lookup", normalize.Apply(result.Combined))
	})

	t.Run("close_pr_dry_run_missing_required_flags_traces_then_refuses", func(t *testing.T) {
		// branch, target, gated-commit, and landing-commit are all validated
		// together as one refusal, so omitting any subset produces the same
		// message -- one scenario (omitting every flag) covers the branch.
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "close-pr", "feature/add-widget", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for missing required flags, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/close_pr_dry_run_missing_required_flags_traces_then_refuses", normalize.Apply(result.Combined))
	})

	t.Run("close_pr_dry_run_empty_branch_traces_then_refuses", func(t *testing.T) {
		// BRANCH is a positional arg an empty string can still satisfy
		// cobra.ExactArgs(1), so the shared validation (and its trace) is what
		// actually catches this, not command-tree wiring.
		setup := env.New(t)
		result := erun.Run(t, []string{
			"exec", "close-pr", "",
			"--target", "main",
			"--remote-url", "https://github.com/sophium/erun.git",
			"--gated-commit", "sourcesha0000000000000000000000000000000",
			"--landing-commit", "landedsha0000000000000000000000000000000",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for an empty branch, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/close_pr_dry_run_empty_branch_traces_then_refuses", normalize.Apply(result.Combined))
	})

	t.Run("close_pr_dry_run_malformed_remote_url_traces_then_refuses", func(t *testing.T) {
		// A non-github.com remote reaches parseGitHubOwnerRepo's rejection
		// branch, distinct from the missing-remote-url case above.
		setup := env.New(t)
		result := erun.Run(t, []string{
			"exec", "close-pr", "feature/add-widget",
			"--target", "main",
			"--remote-url", "https://gitlab.com/sophium/erun.git",
			"--gated-commit", "sourcesha0000000000000000000000000000000",
			"--landing-commit", "landedsha0000000000000000000000000000000",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a non-github remote, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/close_pr_dry_run_malformed_remote_url_traces_then_refuses", normalize.Apply(result.Combined))
	})

	t.Run("close_pr_real_run_fails_cleanly_without_a_token", func(t *testing.T) {
		// No --dry-run, no gh on the scrubbed PATH, and no GITHUB_TOKEN/GH_TOKEN
		// in the environment: the real-run token resolution must refuse before
		// ever reaching the network, naming the fix rather than a raw HTTP error.
		setup := env.New(t)
		result := erun.Run(t, []string{
			"exec", "close-pr", "feature/add-widget",
			"--target", "main",
			"--remote-url", "https://github.com/sophium/erun.git",
			"--gated-commit", "sourcesha0000000000000000000000000000000",
			"--landing-commit", "landedsha0000000000000000000000000000000",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit with no token available, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/close_pr_real_run_fails_cleanly_without_a_token", normalize.Apply(result.Combined))
	})

	t.Run("gate_run_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "gate-run", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/gate_run_help", normalize.Apply(result.Combined))
	})

	t.Run("gate_run_start_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "gate-run", "start", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/gate_run_start_help", normalize.Apply(result.Combined))
	})

	t.Run("gate_run_start_dry_run", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{
			"exec", "gate-run", "start",
			"--source-branch", "feature/add-widget", "--target-branch", "main",
			"--source-commit", "sourcesha0000000000000000000000000000000",
			"--merge-commit", "mergesha00000000000000000000000000000000",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/gate_run_start_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("gate_run_start_no_alias_configured_exits_127", func(t *testing.T) {
		// No erun-type cloud provider alias configured at all -- the exact
		// shape that bit a real merge-queue script: it discarded stderr and
		// had nothing else to detect the failure with. Exit code 127 is the
		// signal that survives that, distinct from an ordinary failure (1).
		setup := env.New(t)
		result := erun.Run(t, []string{
			"exec", "gate-run", "start",
			"--source-branch", "feature/add-widget", "--target-branch", "main",
			"--source-commit", "sourcesha0000000000000000000000000000000",
			"--merge-commit", "mergesha00000000000000000000000000000000",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 127 {
			t.Fatalf("exit %d, want 127: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/gate_run_start_no_alias_configured_exits_127", normalize.Apply(result.Combined))
	})

	t.Run("gate_run_start_dry_run_immediate_failure_classified_inconclusive", func(t *testing.T) {
		// The exact shape that bit a real merge-queue session: a build failing
		// on a ghcr.io TLS handshake timeout is a statement about the network,
		// not the change -- reported failed here should come back inconclusive.
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{
			"exec", "gate-run", "start",
			"--source-branch", "feature/add-widget", "--target-branch", "main",
			"--source-commit", "sourcesha0000000000000000000000000000000",
			"--status", "failed", "--failing-step", "erun build",
			"--log-ref", "failed to solve: failed to resolve source metadata for ghcr.io/sophium/erun-devops:1.0.246: failed to authorize: failed to fetch oauth token: Post \"https://ghcr.io/token\": net/http: TLS handshake timeout",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/gate_run_start_dry_run_immediate_failure_classified_inconclusive", normalize.Apply(result.Combined))
	})

	t.Run("gate_run_start_dry_run_immediate_failure_no_merge_commit", func(t *testing.T) {
		// A squash conflict before any build starts has no trackable running
		// phase at all: --status failed is set directly, with no --merge-commit.
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{
			"exec", "gate-run", "start",
			"--source-branch", "feature/add-widget", "--target-branch", "main",
			"--source-commit", "sourcesha0000000000000000000000000000000",
			"--status", "failed", "--failing-step", "git merge --squash",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/gate_run_start_dry_run_immediate_failure_no_merge_commit", normalize.Apply(result.Combined))
	})

	t.Run("gate_run_start_dry_run_with_review_id_and_log_ref", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{
			"exec", "gate-run", "start",
			"--source-branch", "feature/add-widget", "--target-branch", "main",
			"--source-commit", "sourcesha0000000000000000000000000000000",
			"--merge-commit", "mergesha00000000000000000000000000000000",
			"--review-id", "review-1", "--log-ref", "/tmp/build.json",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/gate_run_start_dry_run_with_review_id_and_log_ref", normalize.Apply(result.Combined))
	})

	t.Run("gate_run_report_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "gate-run", "report", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/gate_run_report_help", normalize.Apply(result.Combined))
	})

	t.Run("gate_run_report_dry_run_passed", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{
			"exec", "gate-run", "report", "gate-run-1", "--status", "passed", "--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/gate_run_report_dry_run_passed", normalize.Apply(result.Combined))
	})

	t.Run("gate_run_report_dry_run_failed_with_failing_step_and_log_ref", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{
			"exec", "gate-run", "report", "gate-run-1",
			"--status", "failed", "--failing-step", "erun build", "--log-ref", "/tmp/build.json",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/gate_run_report_dry_run_failed_with_failing_step_and_log_ref", normalize.Apply(result.Combined))
	})

	t.Run("gate_run_report_no_alias_configured_exits_127", func(t *testing.T) {
		// gate-run start already had this regression scenario; report
		// resolves the platform client through the exact same shared function,
		// but nothing proved it before now -- the roughly twenty gate-run start
		// calls that failed invisibly in a real merge-queue session could just
		// as easily have been report calls instead.
		setup := env.New(t)
		result := erun.Run(t, []string{
			"exec", "gate-run", "report", "gate-run-1", "--status", "passed", "--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 127 {
			t.Fatalf("exit %d, want 127: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/gate_run_report_no_alias_configured_exits_127", normalize.Apply(result.Combined))
	})

	t.Run("gate_run_report_dry_run_failed_classified_inconclusive", func(t *testing.T) {
		// The same known-infrastructure-signature classification as
		// gate-run start: a registry or the network giving up is a statement
		// about the network, not the change, and must not read as a red
		// verdict just because the caller reported it as failed.
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{
			"exec", "gate-run", "report", "gate-run-1",
			"--status", "failed", "--failing-step", "erun build",
			"--log-ref", "failed to solve: failed to resolve source metadata for ghcr.io/sophium/erun-devops:1.0.246: failed to authorize: failed to fetch oauth token: Post \"https://ghcr.io/token\": net/http: TLS handshake timeout",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/gate_run_report_dry_run_failed_classified_inconclusive", normalize.Apply(result.Combined))
	})

	t.Run("gate_run_report_dry_run_failed_log_ref_file_classified_inconclusive", func(t *testing.T) {
		// The merge-queue-drive skill's own rung 4 points --log-ref at a saved
		// `erun build --output json` capture, a file path, never the failure
		// text inline -- so the classifier must read that file's content, not
		// only the literal --log-ref string, or it would never fire on the
		// exact flow it exists to protect.
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		logPath := filepath.Join(setup.Cwd, "build.json")
		if err := os.WriteFile(logPath, []byte(`{"error":"failed to solve: failed to resolve source metadata for ghcr.io/sophium/erun-devops:1.0.246: failed to authorize: failed to fetch oauth token: Post \"https://ghcr.io/token\": net/http: TLS handshake timeout"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		result := erun.Run(t, []string{
			"exec", "gate-run", "report", "gate-run-1",
			"--status", "failed", "--failing-step", "erun build", "--log-ref", logPath,
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/gate_run_report_dry_run_failed_log_ref_file_classified_inconclusive", normalize.Apply(result.Combined))
	})

	t.Run("gate_run_report_dry_run_missing_gate_run_id_traces_then_refuses", func(t *testing.T) {
		// GATE_RUN_ID is a positional arg an empty string can still satisfy
		// cobra.ExactArgs(1), so the shared validation is what actually catches
		// this, not command-tree wiring.
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{
			"exec", "gate-run", "report", "", "--status", "inconclusive", "--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a missing gate run id, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/gate_run_report_dry_run_missing_gate_run_id_traces_then_refuses", normalize.Apply(result.Combined))
	})

	t.Run("reconcile_bypass_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "reconcile-bypass", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/reconcile_bypass_help", normalize.Apply(result.Combined))
	})

	t.Run("reconcile_bypass_dry_run", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{
			"exec", "reconcile-bypass",
			"--remote-url", "https://github.com/sophium/erun.git",
			"--ruleset-id", "11081432", "--target-branch", "main",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/reconcile_bypass_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("reconcile_bypass_dry_run_with_since", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{
			"exec", "reconcile-bypass",
			"--remote-url", "https://github.com/sophium/erun.git",
			"--ruleset-id", "11081432", "--target-branch", "main", "--since", "week",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/reconcile_bypass_dry_run_with_since", normalize.Apply(result.Combined))
	})

	t.Run("reconcile_bypass_dry_run_missing_ruleset_id_traces_then_refuses", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{
			"exec", "reconcile-bypass",
			"--remote-url", "https://github.com/sophium/erun.git",
			"--target-branch", "main",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a missing ruleset id, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/reconcile_bypass_dry_run_missing_ruleset_id_traces_then_refuses", normalize.Apply(result.Combined))
	})

	t.Run("reconcile_bypass_real_run_fails_cleanly_without_a_token", func(t *testing.T) {
		// No gh CLI and no GITHUB_TOKEN/GH_TOKEN in this sandbox: the no-token
		// refusal fires before any network call, so this exercises real-mode
		// resolution up through token lookup without ever reaching GitHub.
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{
			"exec", "reconcile-bypass",
			"--remote-url", "https://github.com/sophium/erun.git",
			"--ruleset-id", "11081432", "--target-branch", "main",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit with no token available, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/reconcile_bypass_real_run_fails_cleanly_without_a_token", normalize.Apply(result.Combined))
	})
}

// githubRulesetStubServer runs a minimal GitHub REST double covering every
// call `exec reconcile-bypass` and `exec plan-ruleset-bypass` make. It is
// reached through ERUN_GITHUB_API_BASE_URL_OVERRIDE, the seam that lets these
// scenarios drive the real wire path -- pagination, per-ruleset filtering,
// push-range expansion, tag lookup, payload construction -- from the compiled
// binary instead of only the dry-run trace.
//
// The fixture models one real window of this repository's own history: a
// gated merge (its tip is a passed gate run's merge commit), a release push
// (three commits, the middle one carrying the release tag, and a tip no gate
// run ever built), a push whose bypass belongs to a different ruleset (must
// be filtered out entirely), and a push nothing accounts for.
type githubRulesetStubOptions struct {
	// RulesetStatus, when non-zero, forces the ruleset read to fail with it.
	RulesetStatus int
	// RuleSuitesStatus, when non-zero, forces the rule-suites list to fail.
	RuleSuitesStatus int
	// HideBypassActors drops bypass_actors from the ruleset response, the
	// shape GitHub returns to a token without write access to the ruleset.
	HideBypassActors bool
	// RefInclude overrides the ruleset's own ref_name include list.
	RefInclude string
	// QueuePermission is what the queue identity's collaborator permission
	// reads as.
	QueuePermission string
}

func githubRulesetStubServer(t testing.TB, opts githubRulesetStubOptions) *httptest.Server {
	t.Helper()
	if opts.RefInclude == "" {
		opts.RefInclude = `"refs/heads/main"`
	}
	if opts.QueuePermission == "" {
		opts.QueuePermission = "write"
	}
	var serverURL string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/sophium/erun/rulesets/rule-suites", func(w http.ResponseWriter, r *http.Request) {
		if opts.RuleSuitesStatus != 0 {
			http.Error(w, `{"message":"Resource not accessible by personal access token"}`, opts.RuleSuitesStatus)
			return
		}
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`[
				{"id":4,"actor_name":"a-human","before_sha":"before-ungated","after_sha":"ungated-tip","pushed_at":"2026-09-02T09:00:00Z","result":"bypass"}
			]`))
			return
		}
		w.Header().Set("Link", `<`+serverURL+`/repos/sophium/erun/rulesets/rule-suites?page=2>; rel="next"`)
		_, _ = w.Write([]byte(`[
			{"id":1,"actor_name":"erun-merge-queue","before_sha":"before-merge","after_sha":"gated-merge-tip","pushed_at":"2026-09-02T12:00:00Z","result":"bypass"},
			{"id":2,"actor_name":"erun-merge-queue","before_sha":"before-release","after_sha":"release-prepare-tip","pushed_at":"2026-09-02T11:00:00Z","result":"bypass"},
			{"id":3,"actor_name":"erun-merge-queue","before_sha":"before-other","after_sha":"other-ruleset-tip","pushed_at":"2026-09-02T10:00:00Z","result":"bypass"}
		]`))
	})
	mux.HandleFunc("GET /repos/sophium/erun/rulesets/rule-suites/{id}", func(w http.ResponseWriter, r *http.Request) {
		rulesetID := "11081432"
		if r.PathValue("id") == "3" {
			rulesetID = "99999999"
		}
		_, _ = w.Write([]byte(`{"rule_evaluations":[
			{"rule_source":{"type":"protected_branch"},"result":"pass","rule_type":"non_fast_forward"},
			{"rule_source":{"type":"ruleset","id":` + rulesetID + `,"name":"main"},"result":"fail","rule_type":"pull_request"},
			{"rule_source":{"type":"ruleset","id":` + rulesetID + `,"name":"main"},"result":"pass","rule_type":"deletion"}
		]}`))
	})
	mux.HandleFunc("GET /repos/sophium/erun/compare/{range}", func(w http.ResponseWriter, r *http.Request) {
		commits := map[string]string{
			"before-release...release-prepare-tip": `[{"sha":"release-commit"},{"sha":"packaging-sync"},{"sha":"release-prepare-tip"}]`,
			"before-ungated...ungated-tip":         `[{"sha":"ungated-tip"}]`,
		}[r.PathValue("range")]
		if commits == "" {
			commits = `[]`
		}
		_, _ = w.Write([]byte(`{"commits":` + commits + `}`))
	})
	mux.HandleFunc("GET /repos/sophium/erun/tags", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"v1.0.247","commit":{"sha":"release-commit"}}]`))
	})
	mux.HandleFunc("GET /repos/sophium/erun/rulesets/11081432", func(w http.ResponseWriter, _ *http.Request) {
		if opts.RulesetStatus != 0 {
			http.Error(w, `{"message":"Not Found"}`, opts.RulesetStatus)
			return
		}
		bypassActors := `,"bypass_actors":[
			{"actor_id":2,"actor_type":"RepositoryRole","bypass_mode":"always"},
			{"actor_id":4,"actor_type":"RepositoryRole","bypass_mode":"always"},
			{"actor_id":5,"actor_type":"RepositoryRole","bypass_mode":"always"}
		]`
		if opts.HideBypassActors {
			bypassActors = ""
		}
		_, _ = w.Write([]byte(`{"id":11081432,"name":"main","target":"branch","enforcement":"active",
			"conditions":{"ref_name":{"include":[` + opts.RefInclude + `],"exclude":[]}},
			"rules":[{"type":"deletion"},{"type":"non_fast_forward"},
				{"type":"pull_request","parameters":{"required_approving_review_count":0}}],
			"current_user_can_bypass":"always"` + bypassActors + `}`))
	})
	mux.HandleFunc("GET /users/{login}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":221100,"login":"` + r.PathValue("login") + `","type":"User"}`))
	})
	mux.HandleFunc("GET /repos/sophium/erun/collaborators/{login}/permission", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"permission":"` + opts.QueuePermission + `"}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	serverURL = server.URL
	return server
}

// gateRunsStubServer answers the one platform call reconcile-bypass makes:
// the PASSED gate runs on the target branch it cross-references against.
func gateRunsStubServer(t testing.TB) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/gate-runs", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"gateRunId": "gr_1", "tenantId": "tenant-1", "sourceBranch": "feature/x", "targetBranch": "main",
			"sourceCommit": "feature-tip", "mergeCommit": "gated-merge-tip", "status": "PASSED",
			"createdAt": "2026-09-02T12:00:00Z", "updatedAt": "2026-09-02T12:00:00Z",
		}})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// stubServerRule collapses one stub server's own base URL to a stable token.
// The kernel assigns its port per run, so a trace naming the URL it called
// could not otherwise have a stable golden -- and doing it per server, rather
// than as a blanket port rule, keeps the deliberately-pinned ports other
// scenarios assert (the port-forward ranges) visible in theirs.
func stubServerRule(server *httptest.Server, token string) normalize.Replacement {
	// The default rules run first and have already turned the host into
	// <LOOPBACK>, so match that form rather than the raw URL.
	normalized := strings.Replace(server.URL, "127.0.0.1", "<LOOPBACK>", 1)
	return normalize.Replacement{Pattern: regexp.MustCompile(regexp.QuoteMeta(normalized)), Token: token}
}

func githubStubEnv(server *httptest.Server) []string {
	return []string{
		"ERUN_GITHUB_API_BASE_URL_OVERRIDE=" + server.URL + "/",
		"GITHUB_TOKEN=gho_stub_token",
	}
}

// TestExecRulesetBypass drives the two ruleset-bypass commands against stub
// GitHub and platform servers: reconcile-bypass's own accounting (a gated
// merge, a release push, a foreign ruleset's bypass, an unaccounted push, an
// unexpected identity) and plan-ruleset-bypass's resolved two-stage edit.
func TestExecRulesetBypass(t *testing.T) {
	t.Run("reconcile_bypass_real_run_accounts_for_a_gated_merge_and_a_release_push", func(t *testing.T) {
		setup := env.New(t)
		github := githubRulesetStubServer(t, githubRulesetStubOptions{})
		platform := gateRunsStubServer(t)
		platformAlias(t, setup, platform)
		envVars := append(setup.Env(), githubStubEnv(github)...)
		result := erun.Run(t, []string{
			"exec", "reconcile-bypass",
			"--remote-url", "https://github.com/sophium/erun.git",
			"--ruleset-id", "11081432", "--target-branch", "main",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit while one push is unaccounted for, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/reconcile_bypass_real_run_accounts_for_a_gated_merge_and_a_release_push",
			normalize.Apply(result.Combined, stubServerRule(github, "<GITHUB_API>"), stubServerRule(platform, "<PLATFORM_API>")))
	})

	t.Run("reconcile_bypass_real_run_flags_a_bypass_by_an_unexpected_identity", func(t *testing.T) {
		// Naming the expected identity is what turns a narrowed bypass grant
		// from configuration into something observable: the human's push is
		// reported UNEXPECTED_ACTOR even though its content is unchanged.
		setup := env.New(t)
		github := githubRulesetStubServer(t, githubRulesetStubOptions{})
		platform := gateRunsStubServer(t)
		platformAlias(t, setup, platform)
		envVars := append(setup.Env(), githubStubEnv(github)...)
		result := erun.Run(t, []string{
			"exec", "reconcile-bypass",
			"--remote-url", "https://github.com/sophium/erun.git",
			"--ruleset-id", "11081432", "--target-branch", "main",
			"--expected-actor", "erun-merge-queue",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for an unexpected bypass identity, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/reconcile_bypass_real_run_flags_a_bypass_by_an_unexpected_identity",
			normalize.Apply(result.Combined, stubServerRule(github, "<GITHUB_API>"), stubServerRule(platform, "<PLATFORM_API>")))
	})

	t.Run("reconcile_bypass_real_run_surfaces_a_github_failure_response", func(t *testing.T) {
		setup := env.New(t)
		github := githubRulesetStubServer(t, githubRulesetStubOptions{RuleSuitesStatus: http.StatusForbidden})
		platform := gateRunsStubServer(t)
		platformAlias(t, setup, platform)
		envVars := append(setup.Env(), githubStubEnv(github)...)
		result := erun.Run(t, []string{
			"exec", "reconcile-bypass",
			"--remote-url", "https://github.com/sophium/erun.git",
			"--ruleset-id", "11081432", "--target-branch", "main",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when github refuses the ledger read, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/reconcile_bypass_real_run_surfaces_a_github_failure_response",
			normalize.Apply(result.Combined, stubServerRule(github, "<GITHUB_API>"), stubServerRule(platform, "<PLATFORM_API>")))
	})

	t.Run("plan_ruleset_bypass_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "plan-ruleset-bypass", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/plan_ruleset_bypass_help", normalize.Apply(result.Combined))
	})

	t.Run("plan_ruleset_bypass_dry_run", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{
			"exec", "plan-ruleset-bypass",
			"--remote-url", "https://github.com/sophium/erun.git",
			"--ruleset-id", "11081432", "--target-branch", "main",
			"--queue-actor", "erun-merge-queue", "--out-dir", "plan",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if _, err := os.Stat(filepath.Join(setup.Cwd, "plan")); !os.IsNotExist(err) {
			t.Fatalf("dry run created the output directory: %v", err)
		}
		golden.Equal(t, "exec/plan_ruleset_bypass_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("plan_ruleset_bypass_dry_run_missing_queue_actor_traces_then_refuses", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{
			"exec", "plan-ruleset-bypass",
			"--remote-url", "https://github.com/sophium/erun.git",
			"--ruleset-id", "11081432",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a missing queue actor, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/plan_ruleset_bypass_dry_run_missing_queue_actor_traces_then_refuses", normalize.Apply(result.Combined))
	})

	t.Run("plan_ruleset_bypass_dry_run_rejects_an_unsupported_actor_type", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{
			"exec", "plan-ruleset-bypass",
			"--remote-url", "https://github.com/sophium/erun.git",
			"--ruleset-id", "11081432", "--queue-actor", "erun-merge-queue",
			"--queue-actor-type", "MachineAccount",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for an unsupported actor type, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/plan_ruleset_bypass_dry_run_rejects_an_unsupported_actor_type", normalize.Apply(result.Combined))
	})

	t.Run("plan_ruleset_bypass_real_run_writes_both_stages_and_the_rollback", func(t *testing.T) {
		setup := env.New(t)
		github := githubRulesetStubServer(t, githubRulesetStubOptions{})
		envVars := append(setup.Env(), githubStubEnv(github)...)
		result := erun.Run(t, []string{
			"exec", "plan-ruleset-bypass",
			"--remote-url", "https://github.com/sophium/erun.git",
			"--ruleset-id", "11081432", "--target-branch", "main",
			"--queue-actor", "erun-merge-queue", "--out-dir", "plan",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/plan_ruleset_bypass_real_run_writes_both_stages_and_the_rollback",
			normalize.Apply(result.Combined, stubServerRule(github, "<GITHUB_API>")))
		// The payload files are the artifact an operator actually applies, and
		// they are a side effect outside the captured streams, so they need
		// their own assertion: the golden above only proves the plan named them.
		for _, stage := range []string{"rollback", "stage1", "stage2"} {
			golden.Equal(t, "exec/plan_ruleset_bypass_payload_"+stage,
				normalize.Apply(mustReadFile(t, filepath.Join(setup.Cwd, "plan", "ruleset-11081432-"+stage+".json"))))
		}
	})

	t.Run("plan_ruleset_bypass_real_run_refuses_when_the_queue_identity_cannot_push", func(t *testing.T) {
		setup := env.New(t)
		github := githubRulesetStubServer(t, githubRulesetStubOptions{QueuePermission: "read"})
		envVars := append(setup.Env(), githubStubEnv(github)...)
		result := erun.Run(t, []string{
			"exec", "plan-ruleset-bypass",
			"--remote-url", "https://github.com/sophium/erun.git",
			"--ruleset-id", "11081432", "--queue-actor", "erun-merge-queue",
			"--out-dir", "plan",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for an identity that cannot push, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/plan_ruleset_bypass_real_run_refuses_when_the_queue_identity_cannot_push",
			normalize.Apply(result.Combined, stubServerRule(github, "<GITHUB_API>")))
	})

	t.Run("plan_ruleset_bypass_real_run_refuses_when_the_ruleset_does_not_govern_the_branch", func(t *testing.T) {
		setup := env.New(t)
		github := githubRulesetStubServer(t, githubRulesetStubOptions{RefInclude: `"refs/heads/release/*"`})
		envVars := append(setup.Env(), githubStubEnv(github)...)
		result := erun.Run(t, []string{
			"exec", "plan-ruleset-bypass",
			"--remote-url", "https://github.com/sophium/erun.git",
			"--ruleset-id", "11081432", "--target-branch", "main",
			"--queue-actor", "erun-merge-queue", "--out-dir", "plan",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a ruleset that does not govern the branch, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/plan_ruleset_bypass_real_run_refuses_when_the_ruleset_does_not_govern_the_branch",
			normalize.Apply(result.Combined, stubServerRule(github, "<GITHUB_API>")))
	})

	t.Run("plan_ruleset_bypass_real_run_refuses_when_github_hides_the_bypass_actors", func(t *testing.T) {
		// GitHub only returns bypass_actors to a token with write access to
		// the ruleset. Planning from a response without them would emit an
		// edit that silently drops every actor the ruleset already has.
		setup := env.New(t)
		github := githubRulesetStubServer(t, githubRulesetStubOptions{HideBypassActors: true})
		envVars := append(setup.Env(), githubStubEnv(github)...)
		result := erun.Run(t, []string{
			"exec", "plan-ruleset-bypass",
			"--remote-url", "https://github.com/sophium/erun.git",
			"--ruleset-id", "11081432", "--queue-actor", "erun-merge-queue",
			"--out-dir", "plan",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when bypass actors are hidden, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/plan_ruleset_bypass_real_run_refuses_when_github_hides_the_bypass_actors",
			normalize.Apply(result.Combined, stubServerRule(github, "<GITHUB_API>")))
	})
}
