package routes

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

type stubCommentRepository struct {
	err error
}

func (s *stubCommentRepository) Create(_ context.Context, comment model.Comment) (model.Comment, error) {
	if s.err != nil {
		return model.Comment{}, s.err
	}
	comment.CommentID = "comment-1"
	return comment, nil
}

func (s *stubCommentRepository) List(context.Context, apirepository.CommentFilter) ([]model.Comment, error) {
	return nil, s.err
}

type stubCommentService struct {
	comment model.Comment
	err     error
}

func (s *stubCommentService) PrepareCreate(_ context.Context, comment model.Comment) (model.Comment, error) {
	if s.err != nil {
		return model.Comment{}, s.err
	}
	return comment, nil
}

func (s *stubCommentService) UpdateStatus(context.Context, string, model.CommentStatus) (model.Comment, error) {
	return s.comment, s.err
}

// TestCreateCommentInvalidCommitIDReportsItsCode: comments.md documents
// INVALID_COMMIT_ID as 400 for a commitId that is not 40 lowercase hex chars.
func TestCreateCommentInvalidCommitIDReportsItsCode(t *testing.T) {
	routes := CommentRoutes{service: &stubCommentService{err: &service.InvalidCommitIDError{CommitID: "bad"}}}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/review-1/comments", bytes.NewBufferString(`{"commitId":"bad"}`))
	req.SetPathValue("review_id", "review-1")
	rec := httptest.NewRecorder()

	routes.createComment(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"INVALID_COMMIT_ID"`) {
		t.Fatalf("body = %q, want code INVALID_COMMIT_ID", rec.Body.String())
	}
}

// TestCreateCommentMalformedJSONReportsInvalidBody: a decode failure gets the
// same INVALID_BODY code documented for a missing/invalid field.
func TestCreateCommentMalformedJSONReportsInvalidBody(t *testing.T) {
	routes := CommentRoutes{service: &stubCommentService{}}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/review-1/comments", bytes.NewBufferString(`{not json`))
	req.SetPathValue("review_id", "review-1")
	rec := httptest.NewRecorder()

	routes.createComment(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"INVALID_BODY"`) {
		t.Fatalf("body = %q, want code INVALID_BODY", rec.Body.String())
	}
}

// TestUpdateCommentStatusAlreadyClosedReportsItsCode: comments.md documents
// ALREADY_CLOSED as 409 for closing an already-closed thread.
func TestUpdateCommentStatusAlreadyClosedReportsItsCode(t *testing.T) {
	routes := CommentRoutes{service: &stubCommentService{err: &service.AlreadyClosedError{CommentID: "comment-1"}}}
	req := httptest.NewRequest(http.MethodPatch, "/v1/reviews/review-1/comments/comment-1/status", bytes.NewBufferString(`{"status":"CLOSED"}`))
	req.SetPathValue("comment_id", "comment-1")
	rec := httptest.NewRecorder()

	routes.updateCommentStatus(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"ALREADY_CLOSED"`) {
		t.Fatalf("body = %q, want code ALREADY_CLOSED", rec.Body.String())
	}
}

// TestCreateCommentBodyConstraintReportsInvalidBody: the comments.body CHECK
// constraint (empty or over 8 KiB) is documented as INVALID_BODY, distinct
// from the generic code a malformed-JSON decode failure would also produce.
func TestCreateCommentBodyConstraintReportsInvalidBody(t *testing.T) {
	routes := CommentRoutes{service: &stubCommentService{err: apirepository.ErrCommentBodyInvalid}}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/review-1/comments", bytes.NewBufferString(`{"commitId":"abcdef0123456789abcdef0123456789abcdef01","body":""}`))
	req.SetPathValue("review_id", "review-1")
	rec := httptest.NewRecorder()

	routes.createComment(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"INVALID_BODY"`) {
		t.Fatalf("body = %q, want code INVALID_BODY", rec.Body.String())
	}
}

// TestListCommentsGenericErrorCarriesAStatusDerivedCode covers the codepath
// with no business-specific code: the envelope still always carries one.
func TestListCommentsGenericErrorCarriesAStatusDerivedCode(t *testing.T) {
	routes := CommentRoutes{comments: &stubCommentRepository{err: apirepository.ErrForbidden}}
	req := httptest.NewRequest(http.MethodGet, "/v1/reviews/review-1/comments", nil)
	req.SetPathValue("review_id", "review-1")
	rec := httptest.NewRecorder()

	routes.listComments(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"FORBIDDEN"`) {
		t.Fatalf("body = %q, want code FORBIDDEN", rec.Body.String())
	}
}
