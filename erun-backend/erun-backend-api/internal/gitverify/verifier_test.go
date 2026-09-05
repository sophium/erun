package gitverify

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runGit runs a git command against dir and fails the test on error, so setup
// code stays readable. These are real local git repositories with no network
// or cluster involved, the same style internal/mergeexec/job_test.go used for
// its own real-git tests before this package replaced it.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append([]string{}, "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com", "HOME="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRemoteRepo creates a local git repository with two commits on branch,
// returning its file:// remote URL and both commit hashes (root, tip).
func newRemoteRepo(t *testing.T, branch string) (remoteURL, root, tip string) {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch="+branch)
	runGit(t, dir, "commit", "--allow-empty", "-m", "root")
	root = runGit(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "commit", "--allow-empty", "-m", "tip")
	tip = runGit(t, dir, "rev-parse", "HEAD")
	return "file://" + dir, root, tip
}

func TestRemoteVerifierContainsTip(t *testing.T) {
	remoteURL, root, tip := newRemoteRepo(t, "main")

	ok, parent, err := NewRemoteVerifier().Contains(context.Background(), remoteURL, "main", tip)
	if err != nil {
		t.Fatalf("Contains: %v", err)
	}
	if !ok {
		t.Fatalf("expected the branch tip to be reported as contained")
	}
	if parent != root {
		t.Fatalf("parent = %q, want the root commit %q", parent, root)
	}
}

func TestRemoteVerifierContainsAncestor(t *testing.T) {
	remoteURL, root, tip := newRemoteRepo(t, "main")
	_ = tip

	ok, parent, err := NewRemoteVerifier().Contains(context.Background(), remoteURL, "main", root)
	if err != nil {
		t.Fatalf("Contains: %v", err)
	}
	if !ok {
		t.Fatalf("expected the root commit to be reported as contained (it is an ancestor of the tip)")
	}
	if parent != "" {
		t.Fatalf("parent = %q, want empty for a root commit", parent)
	}
}

func TestRemoteVerifierRefusesCommitNotOnBranch(t *testing.T) {
	remoteURL, _, _ := newRemoteRepo(t, "main")

	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch=other")
	runGit(t, dir, "commit", "--allow-empty", "-m", "unrelated")
	unrelated := runGit(t, dir, "rev-parse", "HEAD")

	ok, _, err := NewRemoteVerifier().Contains(context.Background(), remoteURL, "main", unrelated)
	if err != nil {
		t.Fatalf("Contains: %v", err)
	}
	if ok {
		t.Fatalf("expected a commit from an unrelated repository to be refused as not contained")
	}
}

func TestRemoteVerifierRefusesUnfetchableRemote(t *testing.T) {
	_, _, tip := newRemoteRepo(t, "main")

	_, _, err := NewRemoteVerifier().Contains(context.Background(), "file://"+filepath.Join(t.TempDir(), "does-not-exist"), "main", tip)
	if err == nil {
		t.Fatalf("expected an error fetching a remote that does not exist")
	}
}

func TestRemoteVerifierRejectsInvalidCommitHash(t *testing.T) {
	remoteURL, _, _ := newRemoteRepo(t, "main")

	_, _, err := NewRemoteVerifier().Contains(context.Background(), remoteURL, "main", "not-a-hash")
	if err == nil {
		t.Fatalf("expected an error for a malformed commit hash")
	}
}

func TestRemoteVerifierIsAncestorForDirectAncestor(t *testing.T) {
	remoteURL, root, tip := newRemoteRepo(t, "main")

	ok, err := NewRemoteVerifier().IsAncestor(context.Background(), remoteURL, "main", root, tip)
	if err != nil {
		t.Fatalf("IsAncestor: %v", err)
	}
	if !ok {
		t.Fatalf("expected the root commit to be reported as an ancestor of the tip")
	}
}

// TestRemoteVerifierIsAncestorTolerantOfCommitsInBetween is the property
// erun#2250 depends on: an ancestor commit stays an ancestor of a
// descendant even when unrelated commits (e.g. a release's own pushes) land
// on the branch between them, unlike a strict immediate-parent comparison.
func TestRemoteVerifierIsAncestorTolerantOfCommitsInBetween(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch=main")
	runGit(t, dir, "commit", "--allow-empty", "-m", "gated tip")
	gatedTip := runGit(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "commit", "--allow-empty", "-m", "release commit 1")
	runGit(t, dir, "commit", "--allow-empty", "-m", "release commit 2")
	reportedCommit := runGit(t, dir, "rev-parse", "HEAD")

	ok, err := NewRemoteVerifier().IsAncestor(context.Background(), "file://"+dir, "main", gatedTip, reportedCommit)
	if err != nil {
		t.Fatalf("IsAncestor: %v", err)
	}
	if !ok {
		t.Fatalf("expected the gated tip to still be reported as an ancestor across the unrelated commits in between")
	}
}

func TestRemoteVerifierIsAncestorTrueForTheSameCommit(t *testing.T) {
	remoteURL, _, tip := newRemoteRepo(t, "main")

	ok, err := NewRemoteVerifier().IsAncestor(context.Background(), remoteURL, "main", tip, tip)
	if err != nil {
		t.Fatalf("IsAncestor: %v", err)
	}
	if !ok {
		t.Fatalf("expected a commit to be reported as an ancestor of itself")
	}
}

func TestRemoteVerifierIsAncestorRefusesWhenAncestorNeverLed(t *testing.T) {
	remoteURL, root, tip := newRemoteRepo(t, "main")

	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch=other")
	runGit(t, dir, "commit", "--allow-empty", "-m", "unrelated")
	unrelated := runGit(t, dir, "rev-parse", "HEAD")

	ok, err := NewRemoteVerifier().IsAncestor(context.Background(), remoteURL, "main", unrelated, tip)
	if err != nil {
		t.Fatalf("IsAncestor: %v", err)
	}
	if ok {
		t.Fatalf("expected a commit from an unrelated repository to be refused as not an ancestor")
	}

	// The reverse direction is also refused: the tip is not an ancestor of
	// its own root.
	ok, err = NewRemoteVerifier().IsAncestor(context.Background(), remoteURL, "main", tip, root)
	if err != nil {
		t.Fatalf("IsAncestor: %v", err)
	}
	if ok {
		t.Fatalf("expected the tip to be refused as an ancestor of its own root")
	}
}

func TestRemoteVerifierIsAncestorRejectsInvalidCommitHash(t *testing.T) {
	remoteURL, _, tip := newRemoteRepo(t, "main")

	_, err := NewRemoteVerifier().IsAncestor(context.Background(), remoteURL, "main", "not-a-hash", tip)
	if err == nil {
		t.Fatalf("expected an error for a malformed ancestor hash")
	}
}
