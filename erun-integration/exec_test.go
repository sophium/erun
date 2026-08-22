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
}
