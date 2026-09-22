package eruncommon

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitOutputForTest returns git's trimmed stdout, so a scenario asserts the
// state it actually reached rather than assuming it.
func gitOutputForTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// newCheckoutBehindItsRemote builds a real checkout of a real bare origin that
// carries `behind` commits the checkout does not, which is the state a machine
// that has not pulled since the last release is in.
func newCheckoutBehindItsRemote(t *testing.T, behind int) string {
	t.Helper()
	dir := t.TempDir()
	initGitRepoAt(t, dir)
	remote := t.TempDir()
	runGitForTest(t, remote, "init", "-q", "--bare", "-b", "main")
	runGitForTest(t, dir, "remote", "add", "origin", remote)
	runGitForTest(t, dir, "push", "-q", "-u", "origin", "main")
	base := gitOutputForTest(t, dir, "rev-parse", "HEAD")
	for index := range behind {
		writeAndCommit(t, dir, fmt.Sprintf("advance-%d.txt", index), "advance\n", "advance the remote")
	}
	runGitForTest(t, dir, "push", "-q", "origin", "main")
	// The checkout rewinds to where it stood before that advance, which is the
	// state a machine that has not pulled since is in.
	runGitForTest(t, dir, "reset", "-q", "--hard", base)
	return dir
}

func annotatePinPlanForTest(t *testing.T, projectRoot string) PinPlan {
	t.Helper()
	plan := PinPlan{Tenant: "frs", Environment: "build", ProjectRoot: projectRoot, Target: "1.0.291"}
	AnnotatePinPlanBaseFreshness(testContext(), &plan)
	return plan
}

// TestPinPlanNamesABaseBehindItsRemote reproduces the reported defect: a pin
// resolved from a checkout behind its remote printed a plan in which every site
// agreed, so a plan computed from a months-old base was indistinguishable from
// a current one. The divergence has to be on the plan itself.
func TestPinPlanNamesABaseBehindItsRemote(t *testing.T) {
	plan := annotatePinPlanForTest(t, newCheckoutBehindItsRemote(t, 3))

	if plan.BaseFreshness == nil {
		t.Fatal("expected the plan to carry how its checkout stands against its remote")
	}
	if plan.BaseFreshness.Behind != 3 {
		t.Fatalf("expected 3 commits behind, got %d", plan.BaseFreshness.Behind)
	}
	if plan.BaseFreshness.Ref != "origin/main" {
		t.Fatalf("expected the divergence named against origin/main, got %q", plan.BaseFreshness.Ref)
	}
	if !plan.BaseFreshness.RemoteRead {
		t.Fatal("expected the remote to have been read, since it is reachable")
	}
	note := plan.BaseFreshnessNote()
	for _, want := range []string{"3 commit(s) behind origin/main", "the plan describes that older base"} {
		if !strings.Contains(note, want) {
			t.Fatalf("expected %q in the note, got %q", want, note)
		}
	}
}

// TestPinPlanKeepsAQuietBaseQuiet is the other half of the contract: a checkout
// level with its remote must not carry the warning, or the warning becomes
// noise every reader learns to skip past.
func TestPinPlanKeepsAQuietBaseQuiet(t *testing.T) {
	plan := annotatePinPlanForTest(t, newCheckoutBehindItsRemote(t, 0))

	if plan.BaseFreshness == nil {
		t.Fatal("expected the plan to state the base it resolved, even when current")
	}
	if plan.BaseFreshness.Behind != 0 {
		t.Fatalf("expected a current checkout, got %d behind", plan.BaseFreshness.Behind)
	}
	if note := plan.BaseFreshnessNote(); note != "" {
		t.Fatalf("expected no warning for a current base, got %q", note)
	}
}

// TestPinPlanRefreshesTheRemoteBeforeCounting pins why the count is taken after
// a fetch rather than from whatever this checkout last observed: a clone whose
// remote-tracking ref is itself stale would otherwise count zero and be
// reported current, which is the same silent staleness one level up.
func TestPinPlanRefreshesTheRemoteBeforeCounting(t *testing.T) {
	dir := newCheckoutBehindItsRemote(t, 2)
	// Rewind the local view of the remote so it agrees with the checkout while
	// the real remote is ahead of both.
	runGitForTest(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	if behind := gitOutputForTest(t, dir, "rev-list", "--count", "HEAD..origin/main"); behind != "0" {
		t.Fatalf("expected the local view of the remote to read 0 behind, got %s", behind)
	}

	plan := annotatePinPlanForTest(t, dir)

	if plan.BaseFreshness == nil || plan.BaseFreshness.Behind != 2 {
		t.Fatalf("expected the fetch to expose the real 2-commit divergence, got %+v", plan.BaseFreshness)
	}
}

// TestPinPlanSaysWhenTheBaseWasOnlyLastSeen covers the weaker answer: with the
// remote unreachable the count is still the best evidence available, but it is
// presented as this checkout's own last look rather than as agreement.
func TestPinPlanSaysWhenTheBaseWasOnlyLastSeen(t *testing.T) {
	dir := newCheckoutBehindItsRemote(t, 1)
	runGitForTest(t, dir, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))

	plan := annotatePinPlanForTest(t, dir)

	if plan.BaseFreshness == nil || plan.BaseFreshness.Behind != 1 {
		t.Fatalf("expected the known divergence to be reported, got %+v", plan.BaseFreshness)
	}
	if plan.BaseFreshness.RemoteRead {
		t.Fatal("expected an unreachable remote to be reported as unread")
	}
	if note := plan.BaseFreshnessNote(); !strings.Contains(note, "as this checkout last saw it") {
		t.Fatalf("expected the weaker evidence to be stated, got %q", note)
	}
}

// TestPinPlanNamesADetachedCheckoutsDivergence covers the checkout an operator
// lands on by inspecting an old commit directly, which has no branch of its own
// to name and so is compared against the remote's default branch.
func TestPinPlanNamesADetachedCheckoutsDivergence(t *testing.T) {
	dir := newCheckoutBehindItsRemote(t, 1)
	runGitForTest(t, dir, "remote", "set-head", "origin", "-a")
	runGitForTest(t, dir, "checkout", "-q", "--detach", "HEAD")

	plan := annotatePinPlanForTest(t, dir)

	if plan.BaseFreshness == nil || plan.BaseFreshness.Behind != 1 {
		t.Fatalf("expected a detached checkout behind its remote to be reported, got %+v", plan.BaseFreshness)
	}
	if plan.BaseFreshness.Branch != "HEAD" {
		t.Fatalf("expected the detached branch to be reported as HEAD, got %q", plan.BaseFreshness.Branch)
	}
}

// TestPinPlanLeavesAnUntrackedCheckoutUnannotated pins that a tree with no
// remote to compare against is not invented into a warning: the absence is a
// real answer, and a pin of a local-only checkout is legitimate.
func TestPinPlanLeavesAnUntrackedCheckoutUnannotated(t *testing.T) {
	dir := t.TempDir()
	initGitRepoAt(t, dir)

	plan := annotatePinPlanForTest(t, dir)

	if plan.BaseFreshness != nil {
		t.Fatalf("expected no freshness verdict for a checkout with no remote, got %+v", plan.BaseFreshness)
	}
	if note := plan.BaseFreshnessNote(); note != "" {
		t.Fatalf("expected no note for a checkout with no remote, got %q", note)
	}
}
