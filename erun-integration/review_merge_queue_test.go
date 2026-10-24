package integration

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testReviewMergeQueueHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"review", "queue", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "review/merge_queue_help", normalize.Apply(result.Combined))
}

func testReviewMergeQueueListDryRun(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{"review", "queue", "list", "--repository", reviewRepository, "--target-branch", "main", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "review/merge_queue_list_dry_run", normalize.Apply(result.Combined))
}

// The reported failure, end to end: one tenant, two repositories, both
// with a `main`. The queue used to be keyed on the target branch alone, so
// each repository's `erun review queue list --target-branch main` printed
// the other's waiting work as its own, and advancing promoted whichever
// review happened to be queued first — into a gate that runs in the
// checkout of a repository whose branch that review does not name.
func testReviewMergeQueueIsTheRepositorysOwn(t *testing.T) {
	setup := env.New(t)
	server := reviewAPIStubServer(t)
	platformAlias(t, setup, server)

	readied := func(name, repository, sourceBranch string) string {
		reviewID := createReviewJSON(t, setup, name, sourceBranch, "main")
		build := erun.Run(t, []string{
			"review", "record-build", reviewID.ReviewID,
			"--commit", "abc123def456abc123def456abc123def456abcd", "--version", "1.2.3", "--output", "json",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if build.ExitCode != 0 {
			t.Fatalf("record-build for %s exit %d: %s", name, build.ExitCode, build.Combined)
		}
		return reviewID.ReviewID
	}
	// createReviewJSON always opens in reviewRepository; the second
	// repository's review is opened directly so both branches — and the
	// name — are spelled the same way in each.
	ours := readied("Land the widget", reviewRepository, "feature/widget")
	otherID := createReadiedOtherRepositoryReview(t, setup)

	assertQueueListsOnlyItsRepository(t, setup, ours, otherID)

	// Advancing names one repository's queue; the other's review stays
	// where it is rather than being promoted by a queue it is not in.
	promoted := erun.Run(t, []string{
		"review", "queue", "advance", "--repository", otherRepository, "--target-branch", "main", "--output", "json",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if promoted.ExitCode != 0 {
		t.Fatalf("advance exit %d: %s", promoted.ExitCode, promoted.Combined)
	}
	var advanced struct {
		ReviewID string `json:"reviewId"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal([]byte(promoted.Stdout), &advanced); err != nil {
		t.Fatalf("decode the promoted review: %v", err)
	}
	if advanced.ReviewID != otherID || advanced.Status != "MERGE" {
		t.Fatalf("promoted %+v, want the named repository's own head %s at MERGE", advanced, otherID)
	}
}

// createReadiedOtherRepositoryReview opens the same name and branches in
// otherRepository and records a successful build so it queues at READY.
func createReadiedOtherRepositoryReview(t *testing.T, setup env.Setup) string {
	t.Helper()
	theirs := erun.Run(t, []string{
		"review", "create", "--name", "Land the widget", "--repository", otherRepository,
		"--source-branch", "feature/widget", "--target-branch", "main", "--output", "json",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if theirs.ExitCode != 0 {
		t.Fatalf("the other repository's create exit %d: %s; the name and branch pair are only reserved within one repository",
			theirs.ExitCode, theirs.Combined)
	}
	var other struct {
		ReviewID string `json:"reviewId"`
	}
	if err := json.Unmarshal([]byte(theirs.Stdout), &other); err != nil {
		t.Fatalf("decode the other repository's review: %v", err)
	}
	build := erun.Run(t, []string{
		"review", "record-build", other.ReviewID,
		"--commit", "abc123def456abc123def456abc123def456abcd", "--version", "1.2.3", "--output", "json",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if build.ExitCode != 0 {
		t.Fatalf("record-build for the other repository exit %d: %s", build.ExitCode, build.Combined)
	}
	return other.ReviewID
}

// assertQueueListsOnlyItsRepository requires each repository's queue list to
// name its own review and leave the other repository's out.
func assertQueueListsOnlyItsRepository(t *testing.T, setup env.Setup, ours, otherID string) {
	t.Helper()
	for _, tc := range []struct {
		repository string
		want       string
		absent     string
	}{
		{reviewRepository, ours, otherID},
		{otherRepository, otherID, ours},
	} {
		listed := erun.Run(t, []string{
			"review", "queue", "list", "--repository", tc.repository, "--target-branch", "main",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if listed.ExitCode != 0 {
			t.Fatalf("queue list for %s exit %d: %s", tc.repository, listed.ExitCode, listed.Combined)
		}
		if !strings.Contains(listed.Combined, tc.want) {
			t.Fatalf("queue list for %s = %q, want it to name %s", tc.repository, listed.Combined, tc.want)
		}
		if strings.Contains(listed.Combined, tc.absent) {
			t.Fatalf("queue list for %s = %q, want the other repository's %s left out", tc.repository, listed.Combined, tc.absent)
		}
	}
}

func testReviewMergeQueueAdvanceEmptyQueueRealRun(t *testing.T) {
	setup := env.New(t)
	server := reviewAPIStubServer(t)
	platformAlias(t, setup, server)
	result := erun.Run(t, []string{"review", "queue", "advance", "--repository", reviewRepository, "--target-branch", "main"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an empty merge queue, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "empty queue") {
		t.Fatalf("expected the stub's empty-queue error to surface, got:\n%s", result.Combined)
	}
}

// merge_queue_advance_names_the_occupying_review drives the reported
// failure end to end: a review already at MERGE (its gate build still
// running) while a second READY review is advanced. The refusal has to
// reach the operator naming the review holding the slot — which is the one
// they must finish or requeue — rather than as a not-found that sends them
// looking for a missing endpoint.
func testReviewMergeQueueAdvanceNamesTheOccupyingReview(t *testing.T) {
	setup := env.New(t)
	server := reviewAPIStubServer(t)
	platformAlias(t, setup, server)

	occupying := createReviewJSON(t, setup, "Land the widget", "feature/widget", "main")
	for _, args := range [][]string{
		{"review", "record-build", occupying.ReviewID, "--commit", "abc123def456abc123def456abc123def456abcd", "--version", "1.2.3"},
		{"review", "queue", "advance", "--repository", reviewRepository, "--target-branch", "main"},
	} {
		if result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()}); result.ExitCode != 0 {
			t.Fatalf("%v exit %d: %s", args, result.ExitCode, result.Combined)
		}
	}

	queued := createReviewJSON(t, setup, "Add the next widget", "feature/next-widget", "main")
	build := erun.Run(t, []string{
		"review", "record-build", queued.ReviewID,
		"--commit", "def456abc123def456abc123def456abc123def4", "--version", "1.2.4",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if build.ExitCode != 0 {
		t.Fatalf("record-build exit %d: %s", build.ExitCode, build.Combined)
	}

	result := erun.Run(t, []string{"review", "queue", "advance", "--repository", reviewRepository, "--target-branch", "main"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit advancing while %s holds MERGE, got:\n%s", occupying.ReviewID, result.Combined)
	}
	if !strings.Contains(result.Combined, occupying.ReviewID) || !strings.Contains(result.Combined, "feature/widget") {
		t.Fatalf("expected the refusal to name the occupying review %s and its source branch, got:\n%s", occupying.ReviewID, result.Combined)
	}
	if !strings.Contains(result.Combined, "requeue") {
		t.Fatalf("expected the refusal to name the requeue remedy, got:\n%s", result.Combined)
	}
	// The client decodes the refusal into its own typed error and renders
	// that; a raw envelope here would mean the message reached the operator
	// as an undecoded body instead of a sentence.
	if strings.Contains(result.Combined, `"code":"MERGE_QUEUE_OCCUPIED"`) {
		t.Fatalf("expected a decoded refusal, got the raw response body:\n%s", result.Combined)
	}
}

func testReviewMergeQueueOverrideAdvanceDryRun(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	args := []string{"review", "queue", "override-advance", "--repository", reviewRepository, "--target-branch", "main", "--reason", "hotfix, reviewers unavailable", "--dry-run"}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "review/merge_queue_override_advance_dry_run", normalize.Apply(result.Combined))
}

func testReviewMergeQueueOverrideAdvanceRequiresReason(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	args := []string{"review", "queue", "override-advance", "--repository", reviewRepository, "--target-branch", "main", "--dry-run"}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a blank --reason, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "a reason is required") {
		t.Fatalf("expected the missing-reason error, got:\n%s", result.Combined)
	}
}

// merge_queue_override_advance_empty_queue_real_run mirrors
// merge_queue_advance_empty_queue_real_run: the stub server has no CLI-only
// path to READY a review (that requires a build result, which this
// double does not model), so this proves the override's real request/
// response round trip — reason included — reaches the server rather than
// only exercising --dry-run's trace branch.
func testReviewMergeQueueOverrideAdvanceEmptyQueueRealRun(t *testing.T) {
	setup := env.New(t)
	server := reviewAPIStubServer(t)
	platformAlias(t, setup, server)
	result := erun.Run(t, []string{
		"review", "queue", "override-advance", "--repository", reviewRepository,
		"--target-branch", "main", "--reason", "hotfix, reviewers unavailable",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an empty merge queue, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "empty queue") {
		t.Fatalf("expected the stub's empty-queue error to surface, got:\n%s", result.Combined)
	}
}
