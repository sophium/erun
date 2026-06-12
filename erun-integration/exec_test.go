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

	t.Run("raw_help", func(t *testing.T) {
		// raw sets DisableFlagParsing, so cobra never intercepts --help on its
		// own. rawCommandWantsHelp must catch it and render help instead of
		// trying to execute a binary called "--help".
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "raw", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/raw_help", normalize.Apply(result.Combined))
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

	t.Run("raw_dry_run_double_dash_passes_flags_through", func(t *testing.T) {
		// Exercises extractDryRunFlag's `--dry-run=true` arm plus the `--`
		// passthrough in both extractDryRunFlag and rawCommandWantsHelp:
		// erun's own --dry-run=true is consumed (no execution, exit 0),
		// while everything after `--` — including the wrapped command's
		// --dry-run and --help — is handed through verbatim, visible in
		// the audit line's argv.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"exec", "raw", "--dry-run=true", "--", "echo", "--dry-run", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/raw_dry_run_double_dash_passes_flags_through", normalize.Apply(result.Combined))
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

	t.Run("diff_files_match_tree_order", func(t *testing.T) {
		// Exercises eruncommon.ParseGitDiff's reorderFilesByTree: the diff
		// panel renders Files while the changed-files list renders Tree, so
		// Files must follow the tree's directory-grouped DFS leaf order. The
		// divergence appears when an untracked file (appended last in git
		// order) belongs to a directory whose first file is a tracked change.
		// Here: dir/a.txt (tracked, modified), root.txt (tracked, modified),
		// dir/b.txt (untracked). Git order is [dir/a.txt, root.txt, dir/b.txt]
		// but the tree groups dir/b.txt under dir/, so Files must come out as
		// [dir/a.txt, dir/b.txt, root.txt] to match the changed-files list.
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

	t.Run("diff_parses_deleted_and_binary_files", func(t *testing.T) {
		// Exercises diffFileParser.parseFileMetadata's "deleted file mode"
		// and "Binary files ... differ" arms: removing a tracked file and
		// rewriting a committed binary in the worktree must surface as
		// status=deleted and binary=true in the parsed --json output.
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
		// Exercises resolveGitDiffReviewBaseBranch's origin/HEAD arm — the
		// symbolic-ref lookup must translate "origin/HEAD" into the real
		// remote default branch name for ReviewBase.Branch — plus
		// parseFileMetadata's "rename from"/"rename to" arms via a committed
		// `git mv` that scope=all diffs against the merge base. The
		// origin/* refs are created locally (update-ref + symbolic-ref) so
		// no network is involved.
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
		golden.Equal(t, "exec/dry_run_with_time_flag_prints_elapsed_on_error", normalize.Apply(result.Combined))
	})
}
