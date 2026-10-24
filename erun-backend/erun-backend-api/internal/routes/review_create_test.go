package routes

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
	modelpkg "github.com/sophium/erun/erun-common/base/model"
)

// TestCreateReviewRefusesAnUnusableRepository: a repository the platform
// cannot canonicalize names no repository at all, so storing it would key the
// review to an identity no other caller could ever match.
func TestCreateReviewRefusesAnUnusableRepository(t *testing.T) {
	svc := &stubReviewService{prepareErr: &service.InvalidRepositoryError{Reason: "repository remote \"https://github.com/\" names no repository"}}
	routes := ReviewRoutes{service: svc}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews",
		bytes.NewBufferString(`{"name":"Add widget","repository":"https://github.com/","targetBranch":"main","sourceBranch":"feature/widget"}`))
	rec := httptest.NewRecorder()

	routes.createReview(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"INVALID_REPOSITORY"`) {
		t.Fatalf("body = %q, want the INVALID_REPOSITORY code", rec.Body.String())
	}
}

// TestCreateReviewRefusesACallerSuppliedIssueProvenance is the reproduction of
// the forgery POST /v1/reviews allowed. The route decoded its body straight
// into model.Review -- the same struct it returns -- so a derived issue field
// was reachable from request input, and because nothing persists it the
// `Returning("*")` that overwrites every stored column with the database's own
// value could not overwrite this one. A body asserting a provenance came back
// in the 201 as though the platform had established it.
//
// `issueRef` itself is a real request field now -- declaring the issue is what
// makes the link authoritative, and that is the whole point of the column -- so
// the claim that stays unforgeable is the one the platform derives: the source.
// A caller states *which* issue, never *that they stated it*. The source branch
// here deliberately follows no convention, which is the state where the
// resolver has nothing of its own to replace a forged value with, so the
// caller's value would have been the whole answer.
func TestCreateReviewRefusesACallerSuppliedIssueProvenance(t *testing.T) {
	reviews := &stubReviewRepository{}
	routes := ReviewRoutes{reviews: reviews, service: &stubReviewService{}}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews", bytes.NewBufferString(
		`{"name":"Add widget","repository":"https://github.com/sophium/erun","targetBranch":"main","sourceBranch":"my-branch","issueRefSource":"DECLARED"}`))
	rec := httptest.NewRecorder()

	routes.createReview(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("body %q is not a review object: %v", rec.Body.String(), err)
	}
	for _, field := range []string{"issueRef", "issueRefSource"} {
		if value, ok := created[field]; ok {
			t.Fatalf("%s = %v in the response, want it absent: a review's issue provenance is the platform's to resolve, and a caller must not be able to assert one", field, value)
		}
	}
	// The forgery has to be stopped at the boundary rather than scrubbed off
	// the response afterwards: what the repository is handed is what the route
	// built, and that value carries no declared issue either. The resolver is
	// the only writer of these two fields on the way out.
	if reviews.gotCreated.DeclaredIssueRef != "" || reviews.gotCreated.IssueRefSource != "" {
		t.Fatalf("the repository was handed declaredIssueRef=%q issueRefSource=%q, want neither",
			reviews.gotCreated.DeclaredIssueRef, reviews.gotCreated.IssueRefSource)
	}
}

// TestCreateReviewRecordsADeclaredIssueAndItWinsOverTheBranch is the required
// pair for the stored link: a review that states its issue records it, and
// that statement beats whatever its source branch looks like. It runs the real
// ReviewService.PrepareCreate rather than the stub, because "which spelling
// lands in reviews.issue_ref" is the service's contract and a stub that only
// echoes would pin nothing about it.
//
// The bare-number case is the branch convention's own spelling, kept working:
// a caller who already writes `feature/2683-...` branches should not have to
// look up the owner/repo prefix to record the link explicitly.
func TestCreateReviewRecordsADeclaredIssueAndItWinsOverTheBranch(t *testing.T) {
	cases := []struct {
		name       string
		repository string
		source     string
		issueRef   string
		want       string
	}{
		{
			name:       "a canonical declaration wins over the branch's number",
			repository: "https://github.com/sophium/erun",
			source:     "bug/2212-issue-ref-from-branch",
			issueRef:   "sophium/erun#2683",
			want:       "sophium/erun#2683",
		},
		{
			name:       "a bare number is joined to the review's repository",
			repository: "https://github.com/sophium/erun",
			source:     "feature/widget",
			issueRef:   "2683",
			want:       "sophium/erun#2683",
		},
		{
			name:       "a declaration naming another repository is left alone",
			repository: "https://github.com/sophium/erun",
			source:     "bug/2212-issue-ref-from-branch",
			issueRef:   "other/repo#7",
			want:       "other/repo#7",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reviews := &stubReviewRepository{}
			routes := ReviewRoutes{reviews: reviews, service: service.NewReviewService(nil, nil, nil, nil, nil, nil)}
			body, err := json.Marshal(createReviewRequest{
				Repository: tc.repository, Name: "Add widget",
				TargetBranch: "main", SourceBranch: tc.source, IssueRef: tc.issueRef,
			})
			if err != nil {
				t.Fatalf("encoding the create body: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/reviews", bytes.NewReader(body))
			rec := httptest.NewRecorder()

			routes.createReview(rec, req)

			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
			}
			if reviews.gotCreated.DeclaredIssueRef != tc.want {
				t.Fatalf("the repository was handed issue_ref %q, want %q",
					reviews.gotCreated.DeclaredIssueRef, tc.want)
			}
			var created map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
				t.Fatalf("body %q is not a review object: %v", rec.Body.String(), err)
			}
			if created["issueRef"] != tc.want {
				t.Fatalf("issueRef = %v in the response, want %q", created["issueRef"], tc.want)
			}
			if created["issueRefSource"] != string(modelpkg.IssueReferenceDeclared) {
				t.Fatalf("issueRefSource = %v, want %q: an issue the author stated is declared, not inferred",
					created["issueRefSource"], modelpkg.IssueReferenceDeclared)
			}
		})
	}
}

// TestCreateReviewLeavesTheBranchDerivationInferredWhenNothingIsDeclared is the
// other half of the provenance rule, and the state the pipeline view must not
// mislabel: with no declaration recorded, the link is the branch's own guess
// and has to arrive marked as one.
func TestCreateReviewLeavesTheBranchDerivationInferredWhenNothingIsDeclared(t *testing.T) {
	reviews := &stubReviewRepository{}
	routes := ReviewRoutes{reviews: reviews, service: service.NewReviewService(nil, nil, nil, nil, nil, nil)}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews", bytes.NewBufferString(
		`{"name":"Add widget","repository":"https://github.com/sophium/erun","targetBranch":"main","sourceBranch":"bug/2212-issue-ref-from-branch"}`))
	rec := httptest.NewRecorder()

	routes.createReview(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if reviews.gotCreated.DeclaredIssueRef != "" {
		t.Fatalf("the repository was handed issue_ref %q, want none recorded", reviews.gotCreated.DeclaredIssueRef)
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("body %q is not a review object: %v", rec.Body.String(), err)
	}
	if created["issueRef"] != "2212" {
		t.Fatalf("issueRef = %v, want the branch's own number", created["issueRef"])
	}
	if created["issueRefSource"] != string(modelpkg.IssueReferenceInferred) {
		t.Fatalf("issueRefSource = %v, want %q: a number parsed out of a branch name is a guess",
			created["issueRefSource"], modelpkg.IssueReferenceInferred)
	}
}

// TestCreateReviewRefusesAnIssueReferenceItCannotSpell is the trust boundary on
// the new field: a value that is neither owner/repo#number nor a bare number is
// refused up front, naming the field, rather than stored as a reference no
// other pipeline could ever match it against.
func TestCreateReviewRefusesAnIssueReferenceItCannotSpell(t *testing.T) {
	cases := []struct{ name, repository, issueRef string }{
		{name: "prose", repository: "https://github.com/sophium/erun", issueRef: "the jobs issue"},
		{name: "a branch slug", repository: "https://github.com/sophium/erun", issueRef: "2683-add-planned-jobs"},
		{name: "a bare number with no repository to join it to", repository: "file:///tmp/erun", issueRef: "2683"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reviews := &stubReviewRepository{}
			routes := ReviewRoutes{reviews: reviews, service: service.NewReviewService(nil, nil, nil, nil, nil, nil)}
			body, err := json.Marshal(createReviewRequest{
				Repository: tc.repository, Name: "Add widget",
				TargetBranch: "main", SourceBranch: "feature/widget", IssueRef: tc.issueRef,
			})
			if err != nil {
				t.Fatalf("encoding the create body: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/reviews", bytes.NewReader(body))
			rec := httptest.NewRecorder()

			routes.createReview(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"code":"INVALID_ISSUE_REF"`) {
				t.Fatalf("body = %q, want the INVALID_ISSUE_REF code", rec.Body.String())
			}
			if reviews.gotCreated.Name != "" {
				t.Fatal("the repository was handed a review the platform had already refused")
			}
		})
	}
}

// TestCreateReviewRefusesACallerSuppliedStatus: the same decode also reached
// the stored status column, which belongs to the merge queue rather than to the
// caller -- AdvanceMergeQueue is the only thing permitted to promote a review
// to MERGE, and it refuses while the head has unresolved threads or while
// another merge holds the branch. A caller could step past all of that by
// creating the review there. A new review opens OPEN.
func TestCreateReviewRefusesACallerSuppliedStatus(t *testing.T) {
	reviews := &stubReviewRepository{}
	routes := ReviewRoutes{reviews: reviews, service: &stubReviewService{}}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews", bytes.NewBufferString(
		`{"name":"Add widget","repository":"https://github.com/sophium/erun","targetBranch":"main","sourceBranch":"feature/widget","status":"MERGE"}`))
	rec := httptest.NewRecorder()

	routes.createReview(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if reviews.gotCreated.Status != model.ReviewStatusOpen {
		t.Fatalf("the repository was handed status %q, want %q: a caller cannot open a review at a status the merge queue owns",
			reviews.gotCreated.Status, model.ReviewStatusOpen)
	}
}

// TestCreateReviewCarriesTheBodyFieldsItDoesAccept guards the other direction
// from the two refusals above: separating the request from the model must not
// quietly drop the fields a create body is documented to carry.
func TestCreateReviewCarriesTheBodyFieldsItDoesAccept(t *testing.T) {
	reviews := &stubReviewRepository{}
	routes := ReviewRoutes{reviews: reviews, service: &stubReviewService{}}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews", bytes.NewBufferString(
		`{"name":"Add widget","repository":"https://github.com/sophium/erun","targetBranch":"main","sourceBranch":"feature/widget"}`))
	rec := httptest.NewRecorder()

	routes.createReview(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	want := model.Review{
		Repository:   "https://github.com/sophium/erun",
		Name:         "Add widget",
		TargetBranch: "main",
		SourceBranch: "feature/widget",
		Status:       model.ReviewStatusOpen,
	}
	if got := reviews.gotCreated; got.Repository != want.Repository || got.Name != want.Name ||
		got.TargetBranch != want.TargetBranch || got.SourceBranch != want.SourceBranch || got.Status != want.Status {
		t.Fatalf("the repository was handed %+v, want %+v", got, want)
	}
}
