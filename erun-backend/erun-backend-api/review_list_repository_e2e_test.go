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

// listedReview is one row of GET /v1/reviews, as a caller reads it.
type listedReview struct {
	ReviewID       string `json:"reviewId"`
	Repository     string `json:"repository"`
	SourceBranch   string `json:"sourceBranch"`
	Status         string `json:"status"`
	IssueRef       string `json:"issueRef"`
	IssueRefSource string `json:"issueRefSource"`
}

// e2eOpenReviewForRepository opens a review naming repository, which is
// canonicalized on the way in exactly as a real client's remote is.
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

// e2eListReviews drives the listing and returns the status, the rows, and the
// raw body so a refusal can be read as well as a listing.
func e2eListReviews(t *testing.T, baseURL, query string) (int, []listedReview, string) {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodGet, "/v1/reviews"+query, nil)
	var listed []listedReview
	if code == http.StatusOK {
		mustNoErr(t, json.Unmarshal([]byte(body), &listed), "parse list")
	}
	return code, listed, body
}

// The reported failure, at the layer a caller drives: a listing narrowed to a
// repository by one of the spellings its remote is held in answered nothing
// for a repository holding two reviews, and a listing narrowed by a bare
// owner/repo shorthand answered the silent subset of that repository its own
// rows happened to match.
func TestReviewListFindsOneRepositoryAcrossEverySpellingOfItsRemote(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv := startMergeQueueAPI(t, config)

	unique := fmt.Sprintf("%d", time.Now().UnixNano())
	// Two remotes for one repository, as erun's own clients hold them: an SSH
	// checkout records git@github.com:<owner>/erun.git, an HTTPS one records
	// https://github.com/<owner>/erun. They are the same repository.
	owner := "spelling-" + unique
	sshRemote := "git@github.com:" + owner + "/erun.git"
	httpsRemote := "https://github.com/" + owner + "/erun"
	otherRemote := "https://github.com/" + owner + "/other"

	e2eOpenReviewForRepository(t, srv.URL, sshRemote, "feature-ssh-"+unique)
	e2eOpenReviewForRepository(t, srv.URL, httpsRemote, "feature-https-"+unique)
	e2eOpenReviewForRepository(t, srv.URL, otherRemote, "feature-other-"+unique)

	// Whichever spelling the caller holds names every row of the repository,
	// and no row of another one.
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

	// The other direction a filter must not widen: a repository holding no
	// review in the asked-for state still answers nothing.
	code, listed, body := e2eListReviews(t, srv.URL, "?repository="+url.QueryEscape(otherRemote)+"&status=MERGED")
	if code != http.StatusOK {
		t.Fatalf("list by the other repository: HTTP %d: %s", code, body)
	}
	if len(listed) != 0 {
		t.Fatalf("a repository with no MERGED review answered %v, want none", listed)
	}

	// The shorthand the report used names no repository -- a bare owner/repo
	// is a repository on whichever forge the caller had in mind -- so it is
	// refused rather than answered with the silent subset it matched.
	code, _, body = e2eListReviews(t, srv.URL, "?repository="+url.QueryEscape(owner+"/erun"))
	if code != http.StatusBadRequest {
		t.Fatalf("list by a bare shorthand: HTTP %d (%s), want 400", code, body)
	}
	if !containsAll(body, "INVALID_REPOSITORY", "git remote get-url origin") {
		t.Fatalf("refusal body = %q, want the INVALID_REPOSITORY code and the form to pass instead", body)
	}
}

// TestReviewIssueRefRoundTripsThroughTheStoredColumn is the stored half of the
// declared issue link, driven through the real API against a real migrated
// PostgreSQL. The declaration is only worth anything if it survives the write:
// the column, bun's mapping of it, and `Returning("*")` are three places a
// value can silently go missing while every in-memory test still passes,
// because an in-memory stub returns whatever it was handed.
//
// Both accepted spellings are covered, and the branch each review proposes
// names a *different* issue than the one declared, so a review that came back
// with the branch's number would be visibly wrong rather than accidentally
// right.
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
		// The branch carries a number the declared reference disagrees with,
		// so a review that came back with the branch's number would be
		// visibly wrong rather than accidentally right. Both names carry the
		// run's own suffix: a scratch database is reused across runs, and a
		// fixed branch name would list the previous run's rows too.
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

		// Read back through the listing, which is a fresh row read rather than
		// the insert's own RETURNING.
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

// TestReviewIssueRefRefusesAnUnspellableDeclaration is the trust boundary at
// the same layer: a value that names no issue in either accepted spelling is
// refused by the real handler, naming the field, rather than stored as a link
// nothing else on the platform could ever match.
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
