package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/command/mergequeue"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testExecPushHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"exec", "push", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/push_help", normalize.Apply(result.Combined))
}

func testExecPushDryRunVerifiesBranchAndTraces(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "push", "main", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/push_dry_run_verifies_branch_and_traces", normalize.Apply(result.Combined))
}

func testExecPushDryRunRefusesBranchMismatch(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "push", "not-main", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a branch mismatch under --dry-run, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/push_dry_run_refuses_branch_mismatch", normalize.Apply(result.Combined))
}

func testExecPushRefusesBranchMismatch(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "push", "not-main"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a branch mismatch, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/push_refuses_branch_mismatch", normalize.Apply(result.Combined))
}

func testExecPushRealRunLandsTheBranchOnTheRemote(t *testing.T) {
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
	var parsed gitx.PushWorkingTreeBranchResult
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
}

func testExecPushDryRunMustNotReachTheNetwork(t *testing.T) {
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
}

func testExecMergeHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"exec", "merge", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/merge_help", normalize.Apply(result.Combined))
}

func testExecMergeDryRunMustNotReachTheNetwork(t *testing.T) {
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
}

func testExecMergeRealRunMergesTheTargetBranch(t *testing.T) {
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
	var parsed mergequeue.MergeWorkingTreeBranchResult
	if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
		t.Fatalf("decode --output json: %v\n%s", err, result.Stdout)
	}
	if parsed.Branch != "feature" || parsed.TargetBranch != "main" || parsed.Remote != "origin" || parsed.Commit == "" {
		t.Fatalf("unexpected result: %+v", parsed)
	}
	if _, err := os.Stat(filepath.Join(setup.Cwd, "main.txt")); err != nil {
		t.Fatalf("expected main.txt to be merged into the feature worktree: %v", err)
	}
}

func testExecMergeConflictRealRunLeavesTheWorktreeMidMerge(t *testing.T) {
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
}

func testExecMergeRealRunRegeneratesConflictingAtlasSum(t *testing.T) {
	// erun#2025: the ordinary `erun exec merge` path (an author syncing a
	// feature branch with main before opening a PR) hits the identical
	// by-construction atlas.sum conflict whenever both branches add a
	// migration. This proves the fix applies there too, not just to the
	// gate-merge batch path.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	seedBareOrigin(t, setup)

	mustWriteFile(t, filepath.Join(setup.Cwd, "migrations", "20260101000000_init.sql"), "CREATE TABLE foo (id int);\n")
	runAtlasHash(t, setup.Cwd)
	fixture.RunGit(t, setup.Cwd, "add", "migrations")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "seed initial migration")
	fixture.RunGit(t, setup.Cwd, "push", "-q", "origin", "main")

	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "feature")
	mustWriteFile(t, filepath.Join(setup.Cwd, "migrations", "20260102000000_add_bar.sql"), "CREATE TABLE bar (id int);\n")
	runAtlasHash(t, setup.Cwd)
	fixture.RunGit(t, setup.Cwd, "add", "migrations")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "feature adds bar")

	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "main")
	mustWriteFile(t, filepath.Join(setup.Cwd, "migrations", "20260103000000_add_baz.sql"), "CREATE TABLE baz (id int);\n")
	runAtlasHash(t, setup.Cwd)
	fixture.RunGit(t, setup.Cwd, "add", "migrations")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "main adds baz")
	fixture.RunGit(t, setup.Cwd, "push", "-q", "origin", "main")
	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "feature")

	envVars := append(setup.Env(), "ERUN_ATLAS_BIN="+atlasBin(t))
	result := erun.Run(t, []string{"exec", "merge", "main", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("expected the merge to land with nothing for a human to resolve, got exit %d:\n%s", result.ExitCode, result.Combined)
	}
	var parsed mergequeue.MergeWorkingTreeBranchResult
	if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
		t.Fatalf("decode --output json: %v\n%s", err, result.Stdout)
	}
	if parsed.Branch != "feature" || parsed.TargetBranch != "main" || parsed.Commit == "" {
		t.Fatalf("unexpected result: %+v", parsed)
	}
	for _, name := range []string{"20260102000000_add_bar.sql", "20260103000000_add_baz.sql"} {
		if _, err := os.Stat(filepath.Join(setup.Cwd, "migrations", name)); err != nil {
			t.Fatalf("expected %s to be present after the merge: %v", name, err)
		}
	}
	status := strings.TrimSpace(captureGit(t, setup.Cwd, "status", "--porcelain"))
	if status != "" {
		t.Fatalf("expected a clean worktree after the merge commit, got status: %q", status)
	}
	if out, ok := captureAtlasValidate(t, setup.Cwd); !ok {
		t.Fatalf("expected the regenerated atlas.sum to validate cleanly, got:\n%s", out)
	}
}
