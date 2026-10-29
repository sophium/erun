package backendapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type listedReview struct {
	ReviewID       string `json:"reviewId"`
	Repository     string `json:"repository"`
	SourceBranch   string `json:"sourceBranch"`
	Status         string `json:"status"`
	IssueRef       string `json:"issueRef"`
	IssueRefSource string `json:"issueRefSource"`
}

func e2eOpenReviewForRepository(t *testing.T, baseURL, repository, sourceBranch string) {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodPost, "/v1/reviews", map[string]any{
		"repository":   repository,
		"name":         "spelled " + sourceBranch,
		"targetBranch": "main",
		"sourceBranch": sourceBranch,
	})
	if code != http.StatusCreated {
		t.Fatalf("create %s: HTTP %d: %s", sourceBranch, code, body)
	}
}

func e2eListReviews(t *testing.T, baseURL, query string) (int, []listedReview, string) {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodGet, "/v1/reviews"+query, nil)
	var listed []listedReview
	if code == http.StatusOK {
		mustNoErr(t, json.Unmarshal([]byte(body), &listed), "parse list")
	}
	return code, listed, body
}

func TestReviewListFindsOneRepositoryAcrossEverySpellingOfItsRemote(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv := startMergeQueueAPI(t, config)

	unique := fmt.Sprintf("%d", time.Now().UnixNano())
	// SSH and HTTPS remotes of one repository.
	owner := "spelling-" + unique
	sshRemote := "git@github.com:" + owner + "/erun.git"
	httpsRemote := "https://github.com/" + owner + "/erun"
	otherRemote := "https://github.com/" + owner + "/other"

	e2eOpenReviewForRepository(t, srv.URL, sshRemote, "feature-ssh-"+unique)
	e2eOpenReviewForRepository(t, srv.URL, httpsRemote, "feature-https-"+unique)
	e2eOpenReviewForRepository(t, srv.URL, otherRemote, "feature-other-"+unique)

	for _, spelling := range []string{sshRemote, httpsRemote} {
		code, listed, body := e2eListReviews(t, srv.URL, "?repository="+url.QueryEscape(spelling))
		if code != http.StatusOK {
			t.Fatalf("list by %s: HTTP %d: %s", spelling, code, body)
		}
		if len(listed) != 2 {
			t.Fatalf("list by %s returned %d rows (%v), want both reviews of the one repository", spelling, len(listed), listed)
		}
		for _, review := range listed {
			if review.Repository != httpsRemote {
				t.Fatalf("list by %s returned a review recorded under %q", spelling, review.Repository)
			}
		}
	}

	code, listed, body := e2eListReviews(t, srv.URL, "?repository="+url.QueryEscape(otherRemote)+"&status=MERGED")
	if code != http.StatusOK {
		t.Fatalf("list by the other repository: HTTP %d: %s", code, body)
	}
	if len(listed) != 0 {
		t.Fatalf("a repository with no MERGED review answered %v, want none", listed)
	}

	// A bare owner/repo names no forge, so it is refused.
	code, _, body = e2eListReviews(t, srv.URL, "?repository="+url.QueryEscape(owner+"/erun"))
	if code != http.StatusBadRequest {
		t.Fatalf("list by a bare shorthand: HTTP %d (%s), want 400", code, body)
	}
	if !containsAll(body, "INVALID_REPOSITORY", "git remote get-url origin") {
		t.Fatalf("refusal body = %q, want the INVALID_REPOSITORY code and the form to pass instead", body)
	}
}

// TestReviewIssueRefRoundTripsThroughTheStoredColumn: the column, bun mapping, and Returning("*")
// can each drop the value while in-memory stubs still pass.
func TestReviewIssueRefRoundTripsThroughTheStoredColumn(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv := startMergeQueueAPI(t, config)

	unique := fmt.Sprintf("%d", time.Now().UnixNano())
	repository := "https://github.com/issue-ref-" + unique + "/erun"
	cases := []struct {
		branch   string
		issueRef string
		want     string
	}{
		// The branch names a different issue than declared, and names are run-unique because
		// the scratch database is reused.
		{branch: "bug/2212-declared-" + unique, issueRef: "issue-ref-" + unique + "/erun#2683", want: "issue-ref-" + unique + "/erun#2683"},
		{branch: "feature/2213-bare-" + unique, issueRef: "2684", want: "issue-ref-" + unique + "/erun#2684"},
	}
	for _, tc := range cases {
		code, body := e2eRequest(t, srv.URL, http.MethodPost, "/v1/reviews", map[string]any{
			"repository":   repository,
			"name":         "declared " + tc.branch,
			"targetBranch": "main",
			"sourceBranch": tc.branch,
			"issueRef":     tc.issueRef,
		})
		if code != http.StatusCreated {
			t.Fatalf("create %s: HTTP %d: %s", tc.branch, code, body)
		}
		var created listedReview
		mustNoErr(t, json.Unmarshal([]byte(body), &created), "parse created review")

		// The listing is a fresh read, not the insert's RETURNING.
		_, listed, listBody := e2eListReviews(t, srv.URL, "?sourceBranch="+url.QueryEscape(tc.branch))
		if len(listed) != 1 {
			t.Fatalf("list %s = %s, want the review that was just created", tc.branch, listBody)
		}
		for name, got := range map[string]string{"created": created.IssueRef, "listed": listed[0].IssueRef} {
			if got != tc.want {
				t.Errorf("%s issueRef = %q, want %q: the declared link is stored canonically, not re-derived from the branch", name, got, tc.want)
			}
		}
		for name, source := range map[string]string{"created": created.IssueRefSource, "listed": listed[0].IssueRefSource} {
			if source != "DECLARED" {
				t.Errorf("%s issueRefSource = %q, want DECLARED", name, source)
			}
		}
	}
}

func TestReviewIssueRefRefusesAnUnspellableDeclaration(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv := startMergeQueueAPI(t, config)

	unique := fmt.Sprintf("%d", time.Now().UnixNano())
	code, body := e2eRequest(t, srv.URL, http.MethodPost, "/v1/reviews", map[string]any{
		"repository":   "https://github.com/issue-ref-" + unique + "/erun",
		"name":         "bad issue ref " + unique,
		"targetBranch": "main",
		"sourceBranch": "feature/2683-bad-issue-ref-" + unique,
		"issueRef":     "the jobs issue",
	})
	if code != http.StatusBadRequest {
		t.Fatalf("create with an unspellable issueRef: HTTP %d, want 400: %s", code, body)
	}
	if !strings.Contains(body, "INVALID_ISSUE_REF") {
		t.Fatalf("body = %q, want the INVALID_ISSUE_REF code", body)
	}
}
