package gitverify

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

const (
	// branchLine is the first line of the paragraph the feature branch adds.
	// The target's own edit reuses it in one of the shapes below, so the
	// comparison has to weigh a whole change set rather than a shared line.
	branchLine = "branch: the edge answered without a session this client recognizes"
	// branchSecondLine is the rest of the branch's paragraph, which the target
	// never carries.
	branchSecondLine = "branch: recovery is to retry the call, or dispatch over the CLI"
	// targetLine is main's own paragraph, added above the branch's.
	targetLine = "main: the resumed job's turn is handed back to it"
)

// lineBlock is a run of numbered lines, so a fixture can insert a paragraph at
// a known place in a file and a shift in the line numbers is a real one.
func lineBlock(prefix string, from, to int) []string {
	lines := make([]string, 0, to-from+1)
	for i := from; i <= to; i++ {
		lines = append(lines, fmt.Sprintf("%s line %d", prefix, i))
	}
	return lines
}

// fileOf joins line groups into newline-terminated file content, the way a
// real text file ends.
func fileOf(groups ...[]string) string {
	var lines []string
	for _, group := range groups {
		lines = append(lines, group...)
	}
	return strings.Join(lines, "\n") + "\n"
}

// branchOverTargetRepo lays down a main and a feature branch that both edit
// shared.md, with the branch's paragraph at the bottom of the file — leaving
// room above it for main's own. The branch also adds a file of its own, which
// the target never touches, so the comparison always has a path that is not in
// contention beside the one that is.
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

// squashLandedOverTargetEditRepo is the shape a squash landing takes when the
// target moves under it: main edits shared.md above the branch's paragraph
// before the commit that lands the branch's work, so the file that work was
// made against is not the file it landed against, while the change to it is
// the same two lines either way. It returns the remote URL, the squash commit
// on main, and the branch's own tip.
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

// targetEditedTheSameFileRepo lands main's own edit in the branch's place
// instead of the branch's work, carrying the same paths, the same actions and
// the same shared line — so the only thing left to tell the two changes apart
// is the rest of what each adds.
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

// TestRemoteVerifierContainsChangesFindsASquashLandedOverTheTargetsOwnEditToTheSameFile
// is the case that made a squash landing unverifiable. main edits a file the
// branch is also editing before the commit that lands the branch's work there,
// so the file's content differs between where the branch forked and the
// squash's parent while the change to it does not. An identity built from the
// blob a path moved to or from reads those as two different changes and
// refuses a landing that happened; an identity built from the lines each
// change adds and removes reads them as one.
func TestRemoteVerifierContainsChangesFindsASquashLandedOverTheTargetsOwnEditToTheSameFile(t *testing.T) {
	remoteURL, squashCommit, branchTip := squashLandedOverTargetEditRepo(t)
	dir := strings.TrimPrefix(remoteURL, "file://")
	verifier := NewRemoteVerifier()

	// The premise, asserted so the fixture cannot quietly stop reproducing
	// the defect: shared.md's blob is not the same on the two sides, which is
	// the whole reason a blob-level identity cannot match here.
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

// TestRemoteVerifierContainsChangesRefusesATargetEditThatIsNotTheBranchs is
// the half the fix must not move. A change's identity is now the lines it adds
// and removes, which is weaker than the blob pair it replaced, so what has to
// hold is that a target whose own commit touches the same paths, in the same
// places, adding the same first line, is still refused — the rest of what it
// adds is all there is left to tell them apart. A test that only ever adds a
// whole missing file would not cross that line.
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

			// The premise: the branch's uncontested file really is identical
			// on both sides, so the contested path is the only thing the
			// comparison can be deciding on.
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

// TestRemoteVerifierContainsChangesFindsAFastForwardedLanding: the branch's
// commit landed by fast-forwarding the target onto the branch tip, leaving
// both refs on the same commit. That is the strongest form of the landing this
// check confirms — the branch is contained in the target by identity — and it
// leaves no change set to compare, so a guard that skips the ancestry question
// whenever the tips are equal refuses exactly the landing it should confirm,
// stranding a review whose work is demonstrably on the target.
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

// TestRemoteVerifierContainsChangesTreatsAnEmptyBranchAsContained pins the
// decision the equal-tip case forces rather than leaving it to be rediscovered.
// A branch cut from the target and never committed to, with the target
// unmoved, is indistinguishable from a fast-forward landing on the two refs
// alone — and a branch that genuinely adds nothing is already reported as
// contained the moment it trails the target instead of sitting exactly on it.
// So there is no refusal left for the guard to buy, and the answer is the one
// that does not strand landed work.
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
