package lifecycle

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
)

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

// newCheckoutBehindItsRemote returns a checkout whose bare origin carries `behind` extra commits.
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
	runGitForTest(t, dir, "reset", "-q", "--hard", base)
	return dir
}

func annotatePinPlanForTest(t *testing.T, projectRoot string) model.PinPlan {
	t.Helper()
	plan := model.PinPlan{Tenant: "frs", Environment: "build", ProjectRoot: projectRoot, Target: "1.0.291"}
	AnnotatePinPlanBaseFreshness(testContext(), &plan)
	return plan
}

// A checkout level with its remote must carry no warning.
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

// A stale remote-tracking ref would otherwise count zero.
func TestPinPlanRefreshesTheRemoteBeforeCounting(t *testing.T) {
	dir := newCheckoutBehindItsRemote(t, 2)
	// Make the local view of origin agree with the checkout while the real remote is ahead.
	runGitForTest(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	if behind := gitOutputForTest(t, dir, "rev-list", "--count", "HEAD..origin/main"); behind != "0" {
		t.Fatalf("expected the local view of the remote to read 0 behind, got %s", behind)
	}

	plan := annotatePinPlanForTest(t, dir)

	if plan.BaseFreshness == nil || plan.BaseFreshness.Behind != 2 {
		t.Fatalf("expected the fetch to expose the real 2-commit divergence, got %+v", plan.BaseFreshness)
	}
}

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

// A detached checkout is compared against the remote's default branch.
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

// A local-only checkout is legitimate and gets no warning.
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
