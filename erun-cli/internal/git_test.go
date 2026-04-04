package internal

import (
	"errors"
	"os/exec"
	"testing"
)

func TestFindCurrentBranch(t *testing.T) {
	repoRoot := t.TempDir()

	for _, args := range [][]string{
		{"init"},
		{"checkout", "-b", "develop"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoRoot
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v (%s)", args, err, output)
		}
	}

	branch, err := FindCurrentBranch(repoRoot)
	if err != nil {
		t.Fatalf("FindCurrentBranch failed: %v", err)
	}
	if branch != "develop" {
		t.Fatalf("expected develop, got %q", branch)
	}
}

func TestFindCurrentBranchOutsideRepository(t *testing.T) {
	_, err := FindCurrentBranch(t.TempDir())
	if !errors.Is(err, ErrNotInGitRepository) {
		t.Fatalf("expected ErrNotInGitRepository, got %v", err)
	}
}
