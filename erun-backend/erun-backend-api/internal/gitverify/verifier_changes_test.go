package gitverify

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// squashLandedRepo returns the remote URL, the squash commit on main, and the branch tip.
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

const (
	branchLine       = "branch: the edge answered without a session this client recognizes"
	branchSecondLine = "branch: recovery is to retry the call, or dispatch over the CLI"
	targetLine       = "main: the resumed job's turn is handed back to it"
)

func lineBlock(prefix string, from, to int) []string {
	lines := make([]string, 0, to-from+1)
	for i := from; i <= to; i++ {
		lines = append(lines, fmt.Sprintf("%s line %d", prefix, i))
	}
	return lines
}

func fileOf(groups ...[]string) string {
	var lines []string
	for _, group := range groups {
		lines = append(lines, group...)
	}
	return strings.Join(lines, "\n") + "\n"
}

func branchOverTargetRepo(t *testing.T) (dir string) {
	t.Helper()
	dir = t.TempDir()
	runGit(t, dir, "init", "--initial-branch=main")
	commitFile(t, dir, "shared.md", fileOf(lineBlock("shared", 1, 10)), "base")

	runGit(t, dir, "checkout", "-b", "feature")
	commitFile(t, dir, "shared.md", fileOf(
		lineBlock("shared", 1, 7), []string{branchLine, branchSecondLine}, lineBlock("shared", 8, 10)),
		"feature: document the session case")
	commitFile(t, dir, "branch-only.txt", "the branch's own file\n", "feature: the branch's own file")

	runGit(t, dir, "checkout", "main")
	return dir
}

// squashLandedOverTargetEditRepo has main edit shared.md above the branch's
// paragraph before the squash lands.
func squashLandedOverTargetEditRepo(t *testing.T) (remoteURL, squashCommit, branchTip string) {
	t.Helper()
	dir := branchOverTargetRepo(t)
	branchTip = runGit(t, dir, "rev-parse", "feature")

	commitFile(t, dir, "shared.md", fileOf(
		lineBlock("shared", 1, 3), []string{targetLine}, lineBlock("shared", 4, 10)),
		"main: the resumed job's turn")

	runGit(t, dir, "merge", "--squash", "feature")
	runGit(t, dir, "commit", "-m", "feature: document the session case (#2717)")
	squashCommit = runGit(t, dir, "rev-parse", "HEAD")
	return "file://" + dir, squashCommit, branchTip
}

// targetEditedTheSameFileRepo lands a main edit sharing paths and first line
// with the branch's work, but not the rest.
func targetEditedTheSameFileRepo(t *testing.T, targetParagraph []string) (remoteURL, landing, branchTip string) {
	t.Helper()
	dir := branchOverTargetRepo(t)
	branchTip = runGit(t, dir, "rev-parse", "feature")

	writeFile(t, dir, "shared.md", fileOf(
		lineBlock("shared", 1, 7), targetParagraph, lineBlock("shared", 8, 10)))
	writeFile(t, dir, "branch-only.txt", "the branch's own file\n")
	runGit(t, dir, "commit", "-m", "main: an unrelated note in the same place")
	landing = runGit(t, dir, "rev-parse", "HEAD")
	return "file://" + dir, landing, branchTip
}

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

func TestRemoteVerifierContainsChangesFindsASquashLandedOverTheTargetsOwnEditToTheSameFile(t *testing.T) {
	remoteURL, squashCommit, branchTip := squashLandedOverTargetEditRepo(t)
	dir := strings.TrimPrefix(remoteURL, "file://")
	verifier := NewRemoteVerifier()

	// Premise: shared.md's blob differs on the two sides.
	mergeBase := runGit(t, dir, "merge-base", "main", "feature")
	before := runGit(t, dir, "rev-parse", mergeBase+":shared.md")
	atSquash := runGit(t, dir, "rev-parse", squashCommit+"^:shared.md")
	if before == atSquash {
		t.Fatalf("fixture no longer reproduces the defect: shared.md is the same blob (%s) at the merge base %s and at the squash's parent", before, mergeBase)
	}

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
		t.Fatalf("expected the branch's work to be reported as contained: main edited the same file above the branch's paragraph before the squash landed it, which does not make it a different change")
	}
	if landed != squashCommit {
		t.Fatalf("landed = %q, want the squash commit %q", landed, squashCommit)
	}
}

func TestRemoteVerifierContainsChangesRefusesATargetEditThatIsNotTheBranchs(t *testing.T) {
	for _, tc := range []struct {
		name            string
		targetParagraph []string
	}{
		{"sharing the branch's first line", []string{branchLine, "main: an unrelated note about the same table"}},
		{"sharing nothing", []string{"main: an unrelated note about the same table", "main: and a second line of it"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remoteURL, _, branchTip := targetEditedTheSameFileRepo(t, tc.targetParagraph)
			dir := strings.TrimPrefix(remoteURL, "file://")

			if branchFile, onMain := runGit(t, dir, "rev-parse", branchTip+":branch-only.txt"), runGit(t, dir, "rev-parse", "main:branch-only.txt"); branchFile != onMain {
				t.Fatalf("fixture no longer isolates the contested file: branch-only.txt is %s on the branch and %s on main", branchFile, onMain)
			}

			contained, landed, err := NewRemoteVerifier().ContainsChanges(context.Background(), remoteURL, "main", "feature")
			if err != nil {
				t.Fatalf("ContainsChanges: %v", err)
			}
			if contained {
				t.Fatalf("expected the branch to be refused: main adds %q where the branch adds %q", tc.targetParagraph, []string{branchLine, branchSecondLine})
			}
			if landed != "" {
				t.Fatalf("landed = %q, want empty for a branch that is not contained", landed)
			}
		})
	}
}

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

func TestRemoteVerifierContainsChangesFindsAFastForwardedLanding(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch=main")
	commitFile(t, dir, "base.txt", "base\n", "base")
	runGit(t, dir, "checkout", "-b", "feature")
	branchTip := commitFile(t, dir, "other.txt", "other\n", "the branch's own work")
	runGit(t, dir, "checkout", "main")
	runGit(t, dir, "merge", "--ff-only", "feature")

	contained, landed, err := NewRemoteVerifier().ContainsChanges(context.Background(), "file://"+dir, "main", "feature")
	if err != nil {
		t.Fatalf("ContainsChanges: %v", err)
	}
	if !contained {
		t.Fatalf("expected a fast-forwarded branch to be reported as contained")
	}
	if landed != branchTip {
		t.Fatalf("landed = %q, want the branch tip %q", landed, branchTip)
	}
}

// TestRemoteVerifierContainsChangesTreatsAnEmptyBranchAsContained: an untouched
// branch is indistinguishable from a fast-forward landing.
func TestRemoteVerifierContainsChangesTreatsAnEmptyBranchAsContained(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch=main")
	base := commitFile(t, dir, "base.txt", "base\n", "base")
	runGit(t, dir, "checkout", "-b", "feature")
	runGit(t, dir, "checkout", "main")

	contained, landed, err := NewRemoteVerifier().ContainsChanges(context.Background(), "file://"+dir, "main", "feature")
	if err != nil {
		t.Fatalf("ContainsChanges: %v", err)
	}
	if !contained {
		t.Fatalf("expected a branch sitting on the target's own tip to be reported as contained")
	}
	if landed != base {
		t.Fatalf("landed = %q, want the target tip %q", landed, base)
	}
}

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
