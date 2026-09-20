package gitverify

import (
	"context"
	"os"
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

// writeFile writes content into dir/name and stages it, so a test can build
// repositories whose commits actually change something — the empty commits
// the Contain/IsAncestor tests get away with carry no change set to compare.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	runGit(t, dir, "add", name)
}

func commitFile(t *testing.T, dir, name, content, message string) string {
	t.Helper()
	writeFile(t, dir, name, content)
	runGit(t, dir, "commit", "-m", message)
	return runGit(t, dir, "rev-parse", "HEAD")
}

// squashLandedRepo builds the shape a GitHub squash merge leaves behind: main
// carries a branch's work as one commit of its own, and none of the branch's
// commits are ancestors of main. It returns the remote URL, the squash commit
// on main, and the branch's own tip.
//
// With unrelatedLanding true, main also advances with a commit of its own
// before the squash — so the squash commit's diff is the branch's work only,
// not the whole span from where the branch forked.
func squashLandedRepo(t *testing.T, unrelatedLanding bool) (remoteURL, squashCommit, branchTip string) {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch=main")
	commitFile(t, dir, "base.txt", "base\n", "base")

	runGit(t, dir, "checkout", "-b", "feature")
	commitFile(t, dir, "feature.txt", "first\n", "feature: first")
	commitFile(t, dir, "feature.txt", "first\nsecond\n", "feature: second")
	branchTip = runGit(t, dir, "rev-parse", "HEAD")

	runGit(t, dir, "checkout", "main")
	if unrelatedLanding {
		commitFile(t, dir, "other.txt", "other\n", "unrelated landing on main")
	}
	runGit(t, dir, "merge", "--squash", "feature")
	runGit(t, dir, "commit", "-m", "feature: first and second (#1)")
	squashCommit = runGit(t, dir, "rev-parse", "HEAD")
	return "file://" + dir, squashCommit, branchTip
}

// TestRemoteVerifierContainsChangesFindsASquashLandedBranch is the case a
// squash-landed review turns on, and it establishes both halves: the check
// report-merged needs — the branch tip being an ancestor of the target —
// really is false for a squash merge, and ContainsChanges still reports the
// work as landed, naming the squash commit.
func TestRemoteVerifierContainsChangesFindsASquashLandedBranch(t *testing.T) {
	remoteURL, squashCommit, branchTip := squashLandedRepo(t, false)
	verifier := NewRemoteVerifier()

	isAncestor, err := verifier.IsAncestor(context.Background(), remoteURL, "main", branchTip, squashCommit)
	if err != nil {
		t.Fatalf("IsAncestor: %v", err)
	}
	if isAncestor {
		t.Fatalf("branch tip %s reported as an ancestor of the squash commit %s: this test no longer reproduces a squash merge", branchTip, squashCommit)
	}

	contained, landed, err := verifier.ContainsChanges(context.Background(), remoteURL, "main", "feature")
	if err != nil {
		t.Fatalf("ContainsChanges: %v", err)
	}
	if !contained {
		t.Fatalf("expected the squash-landed branch's work to be reported as contained in main")
	}
	if landed != squashCommit {
		t.Fatalf("landed = %q, want the squash commit %q", landed, squashCommit)
	}
}

// TestRemoteVerifierContainsChangesTolerantOfUnrelatedCommitsInBetween: the
// squash commit's parent is not where the branch forked, because main moved
// under it. The branch's change set is still exactly what that commit adds,
// which is what the comparison is made of.
func TestRemoteVerifierContainsChangesTolerantOfUnrelatedCommitsInBetween(t *testing.T) {
	remoteURL, squashCommit, _ := squashLandedRepo(t, true)

	contained, landed, err := NewRemoteVerifier().ContainsChanges(context.Background(), remoteURL, "main", "feature")
	if err != nil {
		t.Fatalf("ContainsChanges: %v", err)
	}
	if !contained {
		t.Fatalf("expected the branch to be reported as contained despite main advancing under the squash")
	}
	if landed != squashCommit {
		t.Fatalf("landed = %q, want the squash commit %q", landed, squashCommit)
	}
}

// TestRemoteVerifierContainsChangesFindsAnOrdinaryLanding: a branch that
// really did land by merge commit or fast-forward is answered by the plain
// ancestor case, naming its own tip.
func TestRemoteVerifierContainsChangesFindsAnOrdinaryLanding(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch=main")
	commitFile(t, dir, "base.txt", "base\n", "base")
	runGit(t, dir, "checkout", "-b", "feature")
	branchTip := commitFile(t, dir, "feature.txt", "feature\n", "feature")
	runGit(t, dir, "checkout", "main")
	runGit(t, dir, "merge", "--no-ff", "-m", "merge feature", "feature")

	contained, landed, err := NewRemoteVerifier().ContainsChanges(context.Background(), "file://"+dir, "main", "feature")
	if err != nil {
		t.Fatalf("ContainsChanges: %v", err)
	}
	if !contained {
		t.Fatalf("expected the merge-committed branch to be reported as contained")
	}
	if landed != branchTip {
		t.Fatalf("landed = %q, want the branch tip %q", landed, branchTip)
	}
}

// TestRemoteVerifierContainsChangesRefusesABranchThatNeverLanded: the
// reconciliation verifies rather than believes — an unlanded branch is not
// contained, however much an operator might want it marked MERGED.
func TestRemoteVerifierContainsChangesRefusesABranchThatNeverLanded(t *testing.T) {
	remoteURL, _, _ := squashLandedRepo(t, false)
	dir := strings.TrimPrefix(remoteURL, "file://")
	runGit(t, dir, "checkout", "-b", "unlanded", "main")
	commitFile(t, dir, "unlanded.txt", "never landed\n", "unlanded work")
	runGit(t, dir, "checkout", "main")

	contained, _, err := NewRemoteVerifier().ContainsChanges(context.Background(), remoteURL, "main", "unlanded")
	if err != nil {
		t.Fatalf("ContainsChanges: %v", err)
	}
	if contained {
		t.Fatalf("expected a branch whose work is not in main to be refused")
	}
}

// TestRemoteVerifierContainsChangesRefusesABranchThatAddsNothing: a branch
// sitting exactly where it forked names no landing, so it is refused rather
// than reported as contained on the strength of an empty change set.
func TestRemoteVerifierContainsChangesRefusesABranchThatAddsNothing(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch=main")
	commitFile(t, dir, "base.txt", "base\n", "base")
	runGit(t, dir, "checkout", "-b", "feature")
	commitFile(t, dir, "other.txt", "other\n", "something else lands on main")
	runGit(t, dir, "checkout", "main")
	runGit(t, dir, "merge", "--ff-only", "feature")

	contained, _, err := NewRemoteVerifier().ContainsChanges(context.Background(), "file://"+dir, "main", "feature")
	if err != nil {
		t.Fatalf("ContainsChanges: %v", err)
	}
	if contained {
		t.Fatalf("expected a branch whose tip is the target's own history to be refused, not read as a landing")
	}
}

// TestRemoteVerifierContainsChangesRefusesUnrelatedHistories: no merge base
// means there is no "what this branch added" to match against, so nothing can
// be established and the answer is a refusal rather than a guess.
func TestRemoteVerifierContainsChangesRefusesUnrelatedHistories(t *testing.T) {
	remoteURL, _, _ := squashLandedRepo(t, false)
	dir := strings.TrimPrefix(remoteURL, "file://")
	runGit(t, dir, "checkout", "--orphan", "unrelated")
	commitFile(t, dir, "unrelated.txt", "unrelated\n", "unrelated root")
	runGit(t, dir, "checkout", "main")

	contained, _, err := NewRemoteVerifier().ContainsChanges(context.Background(), remoteURL, "main", "unrelated")
	if err != nil {
		t.Fatalf("ContainsChanges: %v", err)
	}
	if contained {
		t.Fatalf("expected an unrelated branch to be refused")
	}
}

func TestRemoteVerifierContainsChangesRejectsInvalidArgs(t *testing.T) {
	remoteURL, _, _ := squashLandedRepo(t, false)

	for _, tc := range []struct {
		name                      string
		remoteURL, target, source string
	}{
		{"missing remote", "", "main", "feature"},
		{"missing target", remoteURL, "", "feature"},
		{"missing source", remoteURL, "main", ""},
		{"same branch twice", remoteURL, "main", "main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := NewRemoteVerifier().ContainsChanges(context.Background(), tc.remoteURL, tc.target, tc.source); err == nil {
				t.Fatalf("expected an error")
			}
		})
	}
}

func TestRemoteVerifierContainsChangesRefusesUnfetchableRemote(t *testing.T) {
	_, _, _ = squashLandedRepo(t, false)

	_, _, err := NewRemoteVerifier().ContainsChanges(context.Background(),
		"file://"+filepath.Join(t.TempDir(), "does-not-exist"), "main", "feature")
	if err == nil {
		t.Fatalf("expected an error fetching a remote that does not exist")
	}
}
