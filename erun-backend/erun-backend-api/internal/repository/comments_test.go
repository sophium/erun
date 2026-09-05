package repository

import (
	"errors"
	"testing"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestClassifyCommentErrorDistinguishesTheBodyConstraint: comments.md
// documents INVALID_BODY as the machine code for the body length/emptiness
// constraint specifically, so it has to be distinguishable from the other
// check_violation conditions the comments_validate trigger raises.
func TestClassifyCommentErrorDistinguishesTheBodyConstraint(t *testing.T) {
	err := classifyCommentError(&pgconn.PgError{Code: pgerrcode.CheckViolation, ConstraintName: "comments_body_check"})

	if !errors.Is(err, ErrCommentBodyInvalid) {
		t.Fatalf("err = %v, want ErrCommentBodyInvalid", err)
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want it to unwrap to ErrInvalidInput", err)
	}
}

// TestClassifyCommentErrorForeignKeyViolationIsNotFound: a reviewId or
// parentCommentId the caller's tenant cannot see fails the composite foreign
// key the same way a genuinely nonexistent one would, matching the "doesn't
// exist or isn't visible" 404 documented in collaboration/comments.md.
func TestClassifyCommentErrorForeignKeyViolationIsNotFound(t *testing.T) {
	err := classifyCommentError(&pgconn.PgError{Code: pgerrcode.ForeignKeyViolation})

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestClassifyCommentErrorOtherCheckViolationsStayGeneric: a trigger-raised
// check_violation with no constraint name (e.g. "child comments must
// reference the root comment...") must not be mistaken for the body
// constraint.
func TestClassifyCommentErrorOtherCheckViolationsStayGeneric(t *testing.T) {
	err := classifyCommentError(&pgconn.PgError{Code: pgerrcode.CheckViolation, Message: "child comments must reference the root comment for the same review, commit, file, and line"})

	if errors.Is(err, ErrCommentBodyInvalid) {
		t.Fatalf("err = %v, want it not to be classified as the body constraint", err)
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want it to still unwrap to ErrInvalidInput", err)
	}
}
