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

// Rows created before the platform recorded a repository carry none; "no
// repository" is unknown, not a repository of its own (AGENTS.md "Merge Queue").

// An empty repository recreates a legacy row.
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

// An empty repository is the unfiltered promotion.
func e2eAdvanceMergeQueue(t *testing.T, baseURL, repository, targetBranch string) (int, string) {
	t.Helper()
	body := map[string]any{"targetBranch": targetBranch}
	if repository != "" {
		body["repository"] = repository
	}
	return e2eRequest(t, baseURL, http.MethodPost, "/v1/reviews/merge-queue/advance", body)
}

func e2eRequeueToReady(t *testing.T, baseURL, reviewID string) {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodPatch, "/v1/reviews/"+reviewID+"/status", map[string]any{
		"status": "READY",
	})
	if code != http.StatusOK {
		t.Fatalf("requeue %s to READY: HTTP %d: %s", reviewID, code, body)
	}
}

// The absent buildId field, not an empty string, is what is under test.
func e2eReportMergedWithoutBuildID(t *testing.T, baseURL, reviewID, remoteURL string) (int, string) {
	t.Helper()
	return e2eRequest(t, baseURL, http.MethodPatch, "/v1/reviews/"+reviewID+"/status", map[string]any{
		"status":    "MERGED",
		"remoteUrl": remoteURL,
	})
}

// legacyQueueState leaves one review with a repository and one without, both at
// READY on the same target branch.
func legacyQueueState(t *testing.T, baseURL string, remote mergeQueueRemote) (legacyReviewID, namedReviewID string) {
	t.Helper()
	legacyBranch := uniqueBranchName(t, "legacy")
	namedBranch := uniqueBranchName(t, "named")
	remote.branch(t, legacyBranch, "legacy.txt")
	remote.branch(t, namedBranch, "named.txt")

	legacyReviewID = e2eOpenReviewInRepository(t, baseURL, "legacy-repository-e2e", "", remote.main, legacyBranch)
	e2eReportGreenBuild(t, baseURL, legacyReviewID)
	if review := readMergeReview(t, baseURL, legacyReviewID); review.Status != model.ReviewStatusMerge {
		t.Fatalf("legacy review status = %s, want MERGE", review.Status)
	}
	// The named row's repository queue is separate and empty, so it promotes itself too.
	namedReviewID = e2eOpenReviewInRepository(t, baseURL, "legacy-repository-e2e", remote.url, remote.main, namedBranch)
	e2eReportGreenBuild(t, baseURL, namedReviewID)
	if review := readMergeReview(t, baseURL, namedReviewID); review.Status != model.ReviewStatusMerge {
		t.Fatalf("named review status = %s, want MERGE", review.Status)
	}

	e2eRequeueToReady(t, baseURL, legacyReviewID)
	e2eRequeueToReady(t, baseURL, namedReviewID)
	return legacyReviewID, namedReviewID
}

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
	// The legacy row is the queue head, so it is the one promoted.
	var promoted struct {
		ReviewID string `json:"reviewId"`
	}
	mustNoErr(t, json.Unmarshal([]byte(body), &promoted), "parse promoted review")
	if promoted.ReviewID != legacyReviewID {
		t.Fatalf("promoted %s, want the queued legacy review %s — a review recording no repository is still in the queue it was queued in",
			promoted.ReviewID, legacyReviewID)
	}
}

// e2eRemoteOnBranch builds a remote reusing another's branch name, which
// newMergeQueueRemote deliberately avoids.
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
	legacyID := e2eOpenReviewInRepository(t, srv.URL, "two-repositories-e2e", "", remote.main, legacyBranch)
	// Report every build before reading any back: a report can promote a different
	// review (the legacy row spans every repository's queue).
	toRequeue := []struct{ label, reviewID string }{
		{"first", firstID}, {"second", secondID}, {"legacy", legacyID},
	}
	for _, review := range toRequeue {
		code, buildBody := e2eRequest(t, srv.URL, http.MethodPost, "/v1/reviews/"+review.reviewID+"/builds", map[string]any{
			"successful": true,
			"commitId":   fmt.Sprintf("%040x", time.Now().UnixNano()),
			"version":    "0.0.1",
		})
		if code != http.StatusCreated {
			t.Fatalf("report green build for the %s review %s: HTTP %d: %s", review.label, review.reviewID, code, buildBody)
		}
	}
	for _, review := range toRequeue {
		if readMergeReview(t, srv.URL, review.reviewID).Status != model.ReviewStatusMerge {
			continue
		}
		e2eRequeueToReady(t, srv.URL, review.reviewID)
	}

	code, body := e2eAdvanceMergeQueue(t, srv.URL, "", remote.main)
	if code != http.StatusConflict {
		t.Fatalf("advancing an unfiltered queue across two repositories: HTTP %d (want 409): %s", code, body)
	}
	if !containsAll(body, "MERGE_QUEUE_AMBIGUOUS", remote.url, second.url) {
		t.Fatalf("refusal did not name both repositories: %s", body)
	}
	if !strings.Contains(body, legacyID) {
		t.Fatalf("refusal did not name the queued review %s that records no repository: %s", legacyID, body)
	}
	if strings.Contains(body, `"repositories":[""`) || strings.Contains(body, `,""`) {
		t.Fatalf("refusal still lists an empty repository as one of them: %s", body)
	}
}

func TestLegacyReviewAdoptsTheRepositoryItReportsMergedInto(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv := startMergeQueueAPI(t, config)
	remote := newMergeQueueRemote(t)
	sourceBranch := uniqueBranchName(t, "legacy-landed")
	remote.branch(t, sourceBranch, "landed.txt")

	reviewID := e2eOpenReviewInRepository(t, srv.URL, "legacy-landed-e2e", "", remote.main, sourceBranch)

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
