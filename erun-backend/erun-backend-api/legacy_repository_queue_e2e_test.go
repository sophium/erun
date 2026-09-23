package backendapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

// A tenant that already had reviews when the platform started recording a
// repository has two populations in one target branch's queue: rows created
// before carry no repository at all, rows created after carry the canonical
// identity. "No repository recorded" is the absence of an answer, not a second
// answer, so grouping the queue must not read it as a repository of its own —
// see AGENTS.md "Merge Queue".
//
// These gates drive the real API against a real migrated PostgreSQL, the same
// way merge_queue_e2e_test.go does (ERUN_E2E_MERGE_DATABASE_URL).

// e2eOpenReviewInRepository creates a review naming a repository, the form
// every erun client sends. An empty repository recreates a legacy row: one
// created before the platform recorded a repository.
func e2eOpenReviewInRepository(t *testing.T, baseURL, name, repository, targetBranch, sourceBranch string) string {
	t.Helper()
	body := map[string]any{
		"name":         fmt.Sprintf("%s %d", name, time.Now().UnixNano()),
		"targetBranch": targetBranch,
		"sourceBranch": sourceBranch,
	}
	if repository != "" {
		body["repository"] = repository
	}
	code, respBody := e2eRequest(t, baseURL, http.MethodPost, "/v1/reviews", body)
	if code != http.StatusCreated {
		t.Fatalf("create review: HTTP %d (want 201): %s", code, respBody)
	}
	var review mergeReviewResponse
	mustNoErr(t, json.Unmarshal([]byte(respBody), &review), "parse review response")
	return review.ReviewID
}

// e2eAdvanceMergeQueue posts a promotion. An empty repository is the unfiltered
// promotion — a target branch alone, which is what a caller holding no
// repository identity sends.
func e2eAdvanceMergeQueue(t *testing.T, baseURL, repository, targetBranch string) (int, string) {
	t.Helper()
	body := map[string]any{"targetBranch": targetBranch}
	if repository != "" {
		body["repository"] = repository
	}
	return e2eRequest(t, baseURL, http.MethodPost, "/v1/reviews/merge-queue/advance", body)
}

// e2eRequeueToReady returns a review holding MERGE to READY at the tail of its
// target branch queue — the documented missed-merge-window path, and the
// ordinary way a row is left waiting in a queue.
func e2eRequeueToReady(t *testing.T, baseURL, reviewID string) {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodPatch, "/v1/reviews/"+reviewID+"/status", map[string]any{
		"status": "READY",
	})
	if code != http.StatusOK {
		t.Fatalf("requeue %s to READY: HTTP %d: %s", reviewID, code, body)
	}
}

// e2eReportMergedWithoutBuildID is report-merged's documented shape for work
// that landed without the queue: no buildId at all. It is a separate helper
// from e2eReportMerged because the absent field, not an empty string, is the
// thing under test.
func e2eReportMergedWithoutBuildID(t *testing.T, baseURL, reviewID, remoteURL string) (int, string) {
	t.Helper()
	return e2eRequest(t, baseURL, http.MethodPatch, "/v1/reviews/"+reviewID+"/status", map[string]any{
		"status":    "MERGED",
		"remoteUrl": remoteURL,
	})
}

// legacyQueueState builds the reported state: one target branch holding a
// review that records a repository and one that records none, both waiting at
// READY in the queue. Each is promoted to MERGE by its own successful build
// (the queue for the repository it names was empty), then returned to READY —
// which is how a review sits waiting in a queue.
func legacyQueueState(t *testing.T, baseURL string, remote mergeQueueRemote) (legacyReviewID, namedReviewID string) {
	t.Helper()
	legacyBranch := uniqueBranchName(t, "legacy")
	namedBranch := uniqueBranchName(t, "named")
	remote.branch(t, legacyBranch, "legacy.txt")
	remote.branch(t, namedBranch, "named.txt")

	// The legacy row first: it is promoted by the unfiltered queue, which is
	// empty at this point.
	legacyReviewID = e2eOpenReviewInRepository(t, baseURL, "legacy-repository-e2e", "", remote.main, legacyBranch)
	e2eReportGreenBuild(t, baseURL, legacyReviewID)
	if review := readMergeReview(t, baseURL, legacyReviewID); review.Status != model.ReviewStatusMerge {
		t.Fatalf("legacy review status = %s, want MERGE", review.Status)
	}
	// The named row's own repository's queue is a separate, empty one, so it
	// promotes itself too — the split queue the empty repository causes.
	namedReviewID = e2eOpenReviewInRepository(t, baseURL, "legacy-repository-e2e", remote.url, remote.main, namedBranch)
	e2eReportGreenBuild(t, baseURL, namedReviewID)
	if review := readMergeReview(t, baseURL, namedReviewID); review.Status != model.ReviewStatusMerge {
		t.Fatalf("named review status = %s, want MERGE", review.Status)
	}

	e2eRequeueToReady(t, baseURL, legacyReviewID)
	e2eRequeueToReady(t, baseURL, namedReviewID)
	return legacyReviewID, namedReviewID
}

// TestMergeQueueTreatsNoRecordedRepositoryAsUnknown is the reproduction of
// the reported failure: one repository's queue holding a review that records the
// repository plus a legacy review that records none. That is one repository,
// not two, so the unfiltered promotion must not refuse.
func TestMergeQueueTreatsNoRecordedRepositoryAsUnknown(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv := startMergeQueueAPI(t, config)
	remote := newMergeQueueRemote(t)
	legacyReviewID, _ := legacyQueueState(t, srv.URL, remote)

	code, body := e2eAdvanceMergeQueue(t, srv.URL, "", remote.main)
	if code == http.StatusConflict {
		t.Fatalf("advancing a queue holding one repository plus a review with none recorded was refused: HTTP %d: %s", code, body)
	}
	if code != http.StatusOK {
		t.Fatalf("advance: HTTP %d (want 200): %s", code, body)
	}
	// Reachability, not just non-refusal: the legacy row is the queue head, so
	// it is the one that gets promoted rather than being skipped over.
	var promoted struct {
		ReviewID string `json:"reviewId"`
	}
	mustNoErr(t, json.Unmarshal([]byte(body), &promoted), "parse promoted review")
	if promoted.ReviewID != legacyReviewID {
		t.Fatalf("promoted %s, want the queued legacy review %s — a review recording no repository is still in the queue it was queued in",
			promoted.ReviewID, legacyReviewID)
	}
}

// e2eRemoteOnBranch builds a second remote whose target branch carries the
// same name as the first's. newMergeQueueRemote gives every remote a unique
// main so no test's gated-tip bookkeeping is read as another's; two remotes
// sharing a branch name is exactly the state this test needs, so it builds its
// own.
func e2eRemoteOnBranch(t *testing.T, main string) mergeQueueRemote {
	t.Helper()
	bare := t.TempDir()
	runGit(t, bare, "init", "--bare", "--initial-branch="+main)
	seed := t.TempDir()
	runGit(t, seed, "init", "--initial-branch="+main)
	runGit(t, seed, "commit", "--allow-empty", "-m", "root")
	runGit(t, seed, "remote", "add", "origin", "file://"+bare)
	runGit(t, seed, "push", "origin", main)
	return mergeQueueRemote{url: "file://" + bare, main: main}
}

// TestMergeQueueStillRefusesGenuinelyDifferentRepositories is the other half:
// the fix must not collapse real repositories together. Two repositories the
// tenant serves each waiting on the same target branch is exactly what the
// refusal is for.
func TestMergeQueueStillRefusesGenuinelyDifferentRepositories(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv := startMergeQueueAPI(t, config)
	remote := newMergeQueueRemote(t)
	second := e2eRemoteOnBranch(t, remote.main)

	firstBranch := uniqueBranchName(t, "first")
	secondBranch := uniqueBranchName(t, "second")
	remote.branch(t, firstBranch, "first.txt")
	second.branch(t, secondBranch, "second.txt")

	legacyBranch := uniqueBranchName(t, "legacy")
	remote.branch(t, legacyBranch, "legacy.txt")

	firstID := e2eOpenReviewInRepository(t, srv.URL, "two-repositories-e2e", remote.url, remote.main, firstBranch)
	secondID := e2eOpenReviewInRepository(t, srv.URL, "two-repositories-e2e", second.url, second.main, secondBranch)
	// A third row recording no repository, which genuinely belongs to neither
	// of the named repositories as far as the platform can tell.
	legacyID := e2eOpenReviewInRepository(t, srv.URL, "two-repositories-e2e", "", remote.main, legacyBranch)
	for label, reviewID := range map[string]string{"first": firstID, "second": secondID, "legacy": legacyID} {
		code, buildBody := e2eRequest(t, srv.URL, http.MethodPost, "/v1/reviews/"+reviewID+"/builds", map[string]any{
			"successful": true,
			"commitId":   fmt.Sprintf("%040x", time.Now().UnixNano()),
			"version":    "0.0.1",
		})
		if code != http.StatusCreated {
			t.Fatalf("report green build for the %s review %s: HTTP %d: %s", label, reviewID, code, buildBody)
		}
		// A review promoted to MERGE goes back to READY; one whose promotion
		// was blocked by a slot already held elsewhere in the queue is
		// already waiting where it belongs.
		if readMergeReview(t, srv.URL, reviewID).Status == model.ReviewStatusMerge {
			if code, body := e2eRequest(t, srv.URL, http.MethodPatch, "/v1/reviews/"+reviewID+"/status", map[string]any{"status": "READY"}); code != http.StatusOK {
				t.Fatalf("requeue the %s review %s to READY: HTTP %d: %s", label, reviewID, code, body)
			}
		}
	}

	code, body := e2eAdvanceMergeQueue(t, srv.URL, "", remote.main)
	if code != http.StatusConflict {
		t.Fatalf("advancing an unfiltered queue across two repositories: HTTP %d (want 409): %s", code, body)
	}
	if !containsAll(body, "MERGE_QUEUE_AMBIGUOUS", remote.url, second.url) {
		t.Fatalf("refusal did not name both repositories: %s", body)
	}
	// The row that belongs to neither is named by id, so the operator is not
	// left to infer which waiting review is the unattributable one.
	if !strings.Contains(body, legacyID) {
		t.Fatalf("refusal did not name the queued review %s that records no repository: %s", legacyID, body)
	}
	if strings.Contains(body, `"repositories":[""`) || strings.Contains(body, `,""`) {
		t.Fatalf("refusal still lists an empty repository as one of them: %s", body)
	}
}

// TestLegacyReviewAdoptsTheRepositoryItReportsMergedInto covers the other half
// of the same failure: a legacy row's work that landed without the queue must still
// be reportable, without a buildId, and adopting the repository the report
// names.
func TestLegacyReviewAdoptsTheRepositoryItReportsMergedInto(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv := startMergeQueueAPI(t, config)
	remote := newMergeQueueRemote(t)
	sourceBranch := uniqueBranchName(t, "legacy-landed")
	remote.branch(t, sourceBranch, "landed.txt")

	reviewID := e2eOpenReviewInRepository(t, srv.URL, "legacy-landed-e2e", "", remote.main, sourceBranch)

	// The work lands without the queue: squash-merged straight onto the target.
	remote.merge(t, remote.main, sourceBranch, "squash "+sourceBranch)

	code, body := e2eReportMergedWithoutBuildID(t, srv.URL, reviewID, remote.url)
	if code != http.StatusOK {
		t.Fatalf("report-merged without a buildId for a legacy review: HTTP %d (want 200): %s", code, body)
	}
	var review struct {
		Status     model.ReviewStatus `json:"status"`
		Repository string             `json:"repository"`
	}
	mustNoErr(t, json.Unmarshal([]byte(body), &review), "parse review response")
	if review.Status != model.ReviewStatusMerged {
		t.Fatalf("status = %s, want MERGED", review.Status)
	}
	if review.Repository == "" {
		t.Fatalf("a review reported merged through %s did not adopt it as its repository", remote.url)
	}
}

func containsAll(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) {
			return false
		}
	}
	return true
}
