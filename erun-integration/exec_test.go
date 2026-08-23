package integration

import (
	"encoding/json"
	"os"
	osexec "os/exec"
	"path/filepath"
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
}
