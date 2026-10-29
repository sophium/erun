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

// The issue source is derived by the platform; a caller can name an issue but not its provenance.
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
	// The route must not hand the repository a declared issue it did not build.
	if reviews.gotCreated.DeclaredIssueRef != "" || reviews.gotCreated.IssueRefSource != "" {
		t.Fatalf("the repository was handed declaredIssueRef=%q issueRefSource=%q, want neither",
			reviews.gotCreated.DeclaredIssueRef, reviews.gotCreated.IssueRefSource)
	}
}

// Runs the real PrepareCreate: which spelling lands in reviews.issue_ref is the service's contract.
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

// Status belongs to the merge queue, so a new review opens OPEN regardless of the body.
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
