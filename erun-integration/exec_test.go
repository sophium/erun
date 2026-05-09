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

	t.Run("raw_dry_run_traces_inside_project", func(t *testing.T) {
		// Exercises eruncommon.RunRawCommand: with a real project root
		// resolved, the dry-run trace must show the resolved cwd and the
		// raw command before the runner short-circuits.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "raw", "echo", "hello", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "exec/raw_dry_run_traces_inside_project", normalize.Apply(result.Combined))
	})

	t.Run("raw_dry_run_redacts_sensitive_args", func(t *testing.T) {
		// Exercises feedback_render.go redactAuditArgs and
		// eruncommon.RunRawCommand argument redaction: --token and
		// --password values must appear as <redacted> in both the audit
		// line and the raw-command trace line.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "raw", "--dry-run", "curl", "https://example", "--token", "secret-value", "--password=hidden", "ok"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "exec/raw_dry_run_redacts_sensitive_args", normalize.Apply(result.Combined))
	})

	t.Run("diff_dry_run_traces_git_diff", func(t *testing.T) {
		// Exercises exec.go runExecDiffCommand: --dry-run must trace the
		// `git diff --no-color --no-ext-diff` command line for the resolved
		// project root.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "diff", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "exec/diff_dry_run_traces_git_diff", normalize.Apply(result.Combined))
	})

	t.Run("diff_dry_run_errors_outside_git_project", func(t *testing.T) {
		// Exercises exec.go runExecDiffCommand error path: outside a git
		// project, findProjectRoot fails and the audit line surfaces the
		// `cannot find git project` message before any side effect runs.
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "diff", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit outside a git project, got 0:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "cannot find git project") {
			t.Errorf("expected 'cannot find git project' message, got:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/diff_dry_run_errors_outside_git_project", normalize.Apply(result.Combined))
	})

	t.Run("diff_json_emits_structured_result", func(t *testing.T) {
		// Exercises eruncommon.ResolveGitDiff + ParseGitDiff: modifying a
		// tracked file then running `exec diff --json` must emit a parsed
		// DiffResult with summary, files, and tree populated.
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
		// Exercises eruncommon.appendUntrackedGitDiff: a brand-new file in
		// the worktree must show up in the parsed Files list when the diff
		// runs in default scope.
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

	t.Run("diff_scope_all_traces_review_base", func(t *testing.T) {
		// Exercises eruncommon.ResolveGitDiffWithOptions + review-base
		// resolution: with a follow-up commit and a worktree change,
		// `--scope=all --json` must populate ReviewBase and ReviewCommits
		// against local main.
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
		// Exercises eruncommon.gitDiffReviewArgs scope=commit branch:
		// `--scope=commit --selected-commit=<hash>` must run
		// `git diff <hash>^` so the parsed diff covers commits since the
		// selected commit's parent.
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

	t.Run("diff_raw_output_includes_deletions", func(t *testing.T) {
		// Exercises eruncommon.WriteRawDiff and appendDeletedLine: removing
		// the seeded README content and writing fresh lines must produce a
		// raw diff with both '-' and '+' hunk lines on stdout.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		mustWriteFile(t, filepath.Join(setup.Cwd, "README.md"), "rewritten\n")
		result := erun.Run(t, []string{"exec", "diff"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Stdout, "diff --git a/README.md") {
			t.Errorf("expected raw diff header on stdout, got:\n%s", result.Stdout)
		}
		if !strings.Contains(result.Stdout, "-# test") {
			t.Errorf("expected deleted-line marker '-# test' in raw diff, got:\n%s", result.Stdout)
		}
		if !strings.Contains(result.Stdout, "+rewritten") {
			t.Errorf("expected added-line marker '+rewritten' in raw diff, got:\n%s", result.Stdout)
		}
		golden.Equal(t, "exec/diff_raw_output_includes_deletions", normalize.Apply(result.Combined))
	})

	t.Run("diff_selected_commit_without_scope_errors", func(t *testing.T) {
		// Exercises exec.go resolveExecDiff guard: --selected-commit without
		// --scope=commit must fail before any git command runs.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "diff", "--selected-commit=abc1234"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "--selected-commit requires --scope=commit") {
			t.Errorf("expected scope guard error, got:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/diff_selected_commit_without_scope_errors", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_time_flag_prints_elapsed_on_error", func(t *testing.T) {
		// Exercises feedback_render.go printElapsedTime error path: when
		// --time is set and the command fails, the `elapsed:` line must
		// still appear on stderr. Driving this through `exec diff --dry-run`
		// outside a git project keeps the run side-effect-free.
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "diff", "--dry-run", "--time"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit (no git project), got 0:\n%s", result.Combined)
		}
		if !strings.Contains(result.Stderr, "elapsed:") {
			t.Errorf("expected --time to print elapsed even on error, got stderr:\n%s", result.Stderr)
		}
		golden.Equal(t, "exec/dry_run_with_time_flag_prints_elapsed_on_error", normalize.Apply(result.Combined))
	})
}
