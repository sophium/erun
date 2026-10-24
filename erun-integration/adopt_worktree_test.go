package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// stageAdoptVolumes lays out the two staging mounts the runtime chart's
// adopt-worktree init container sees: the home volume holding a legacy
// checkout, and the empty worktree claim beside it.
func stageAdoptVolumes(t *testing.T, root string) (legacy, claim string) {
	t.Helper()
	legacy = filepath.Join(root, "mnt", "erun-home", "git", "petios")
	claim = filepath.Join(root, "mnt", "erun-worktree")
	mustWriteFile(t, filepath.Join(legacy, ".git", "HEAD"), "ref: refs/heads/main\n")
	mustWriteFile(t, filepath.Join(legacy, "src", "main.go"), "package main\n")
	if err := os.MkdirAll(claim, 0o755); err != nil {
		t.Fatal(err)
	}
	return legacy, claim
}

// normalizeAdoptOutput names the staged volumes relative to the scenario's own
// root before the default temp-dir rule collapses each path to one token, so
// the golden still shows which path is the legacy tree, the claim, the
// staging dir and the preserved copy.
func normalizeAdoptOutput(root, output string) string {
	output = strings.ReplaceAll(filepath.ToSlash(output), filepath.ToSlash(root), "<STAGE>")
	return normalize.Apply(output)
}

func TestAdoptWorktree(t *testing.T) {
	t.Parallel()
	t.Run("help", testAdoptWorktreeHelp)
	t.Run("dry_run_names_the_copy_and_set_aside_without_touching_either_volume", testAdoptWorktreeDryRunNamesTheCopyAndSetAsideWithoutTouchingEitherVolume)
	t.Run("real_run_adopts_then_a_second_boot_is_a_no_op", testAdoptWorktreeRealRunAdoptsThenASecondBootIsANoOp)
	t.Run("json_result_reports_the_outcome", testAdoptWorktreeJsonResultReportsTheOutcome)
	t.Run("missing_claim_argument_is_a_usage_error", testAdoptWorktreeMissingClaimArgumentIsAUsageError)
}

func testAdoptWorktreeHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"adopt-worktree", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "adopt-worktree/help", normalize.Apply(result.Combined))
}

func testAdoptWorktreeDryRunNamesTheCopyAndSetAsideWithoutTouchingEitherVolume(t *testing.T) {
	// The init container's decision, previewed: an empty claim and a real
	// legacy repository adopt, and the dry run says what it would copy,
	// move and set aside while leaving both volumes as they were.
	setup := env.New(t)
	root := t.TempDir()
	legacy, claim := stageAdoptVolumes(t, root)
	result := erun.Run(t, []string{"adopt-worktree", "--dry-run", legacy, claim}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if entries, _ := os.ReadDir(claim); len(entries) != 0 {
		t.Fatalf("a dry run copied onto the worktree volume: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(legacy, ".git", "HEAD")); err != nil {
		t.Fatalf("a dry run moved the legacy tree: %v", err)
	}
	golden.Equal(t, "adopt-worktree/dry_run_names_the_copy_and_set_aside_without_touching_either_volume", normalizeAdoptOutput(root, result.Combined))
}

func testAdoptWorktreeRealRunAdoptsThenASecondBootIsANoOp(t *testing.T) {
	// The first boot after the claim appears adopts the checkout and
	// keeps the original beside it; every later boot finds the claim
	// populated and leaves it alone.
	setup := env.New(t)
	root := t.TempDir()
	legacy, claim := stageAdoptVolumes(t, root)
	first := erun.Run(t, []string{"adopt-worktree", legacy, claim}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if first.ExitCode != 0 {
		t.Fatalf("exit %d: %s", first.ExitCode, first.Combined)
	}
	if _, err := os.Stat(filepath.Join(claim, "src", "main.go")); err != nil {
		t.Fatalf("the checkout did not land on the worktree volume: %v", err)
	}
	if _, err := os.Stat(filepath.Join(legacy+".pre-worktree-volume", ".git", "HEAD")); err != nil {
		t.Fatalf("the original was not preserved: %v", err)
	}
	second := erun.Run(t, []string{"adopt-worktree", legacy, claim}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if second.ExitCode != 0 {
		t.Fatalf("exit %d: %s", second.ExitCode, second.Combined)
	}
	golden.Equal(t, "adopt-worktree/real_run_adopts_then_a_second_boot_is_a_no_op",
		normalizeAdoptOutput(root, first.Combined+second.Combined))
}

func testAdoptWorktreeJsonResultReportsTheOutcome(t *testing.T) {
	setup := env.New(t)
	root := t.TempDir()
	legacy, claim := stageAdoptVolumes(t, root)
	if err := os.RemoveAll(filepath.Join(legacy, ".git")); err != nil {
		t.Fatal(err)
	}
	result := erun.Run(t, []string{"--output", "json", "adopt-worktree", legacy, claim}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "adopt-worktree/json_result_reports_the_outcome", normalizeAdoptOutput(root, result.Stdout))
}

func testAdoptWorktreeMissingClaimArgumentIsAUsageError(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"adopt-worktree", "/mnt/erun-home/git/petios"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a usage error, got exit 0:\n%s", result.Combined)
	}
	golden.Equal(t, "adopt-worktree/missing_claim_argument_is_a_usage_error", normalize.Apply(result.Combined))
}
