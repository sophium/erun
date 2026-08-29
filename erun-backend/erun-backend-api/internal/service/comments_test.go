package service

import (
	"context"
	"errors"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type fakeCommentRepo struct {
	comments map[string]model.Comment
}

func (f *fakeCommentRepo) Create(_ context.Context, comment model.Comment) (model.Comment, error) {
	return comment, nil
}

func (f *fakeCommentRepo) Get(_ context.Context, commentID string) (model.Comment, error) {
	c, ok := f.comments[commentID]
	if !ok {
		return model.Comment{}, repository.ErrNotFound
	}
	return c, nil
}

func (f *fakeCommentRepo) Update(_ context.Context, comment model.Comment) (model.Comment, error) {
	f.comments[comment.CommentID] = comment
	return comment, nil
}

func withTestSecurity(ctx context.Context) context.Context {
	return security.WithContext(ctx, security.Context{TenantID: "tenant-1", ErunUserID: "user-1"})
}

// TestPrepareCreateRefusesAMalformedCommitID: the 40-lowercase-hex-character
// grammar collaboration/comments.md documents as INVALID_COMMIT_ID.
func TestPrepareCreateRefusesAMalformedCommitID(t *testing.T) {
	svc := NewCommentService(&fakeCommentRepo{})
	_, err := svc.PrepareCreate(withTestSecurity(context.Background()), model.Comment{CommitID: "not-a-commit"})

	var invalidCommitID *InvalidCommitIDError
	if !errors.As(err, &invalidCommitID) {
		t.Fatalf("PrepareCreate error = %v, want *InvalidCommitIDError", err)
	}
	if !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("PrepareCreate error = %v, want it to unwrap to ErrInvalidInput", err)
	}
}

// TestPrepareCreateAcceptsAWellFormedCommitID is the companion positive case:
// the validation must not reject the exact grammar it documents.
func TestPrepareCreateAcceptsAWellFormedCommitID(t *testing.T) {
	svc := NewCommentService(&fakeCommentRepo{})
	comment, err := svc.PrepareCreate(withTestSecurity(context.Background()), model.Comment{
		CommitID: "abcdef0123456789abcdef0123456789abcdef01",
	})
	if err != nil {
		t.Fatalf("PrepareCreate: %v", err)
	}
	if comment.CreatorUserID != "user-1" {
		t.Fatalf("CreatorUserID = %q, want it set from the security context", comment.CreatorUserID)
	}
}

// TestUpdateStatusRefusesClosingAnAlreadyClosedThread: comments.md documents
// ALREADY_CLOSED as a 409, distinct from every other UpdateStatus failure.
func TestUpdateStatusRefusesClosingAnAlreadyClosedThread(t *testing.T) {
	repo := &fakeCommentRepo{comments: map[string]model.Comment{
		"c1": {CommentID: "c1", Status: model.CommentStatusClosed},
	}}
	svc := NewCommentService(repo)

	_, err := svc.UpdateStatus(context.Background(), "c1", model.CommentStatusClosed)

	var alreadyClosed *AlreadyClosedError
	if !errors.As(err, &alreadyClosed) {
		t.Fatalf("UpdateStatus error = %v, want *AlreadyClosedError", err)
	}
	if !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("UpdateStatus error = %v, want it to unwrap to ErrConflict", err)
	}
}

// TestUpdateStatusAllowsReopeningAClosedThread: only closing an
// already-closed thread is refused — reopening it is a normal transition.
func TestUpdateStatusAllowsReopeningAClosedThread(t *testing.T) {
	repo := &fakeCommentRepo{comments: map[string]model.Comment{
		"c1": {CommentID: "c1", Status: model.CommentStatusClosed},
	}}
	svc := NewCommentService(repo)

	updated, err := svc.UpdateStatus(context.Background(), "c1", model.CommentStatusOpen)
	if err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if updated.Status != model.CommentStatusOpen {
		t.Fatalf("status = %s, want OPEN", updated.Status)
	}
}
