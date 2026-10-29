package repository

import (
	"errors"
	"testing"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestClassifyCommentErrorDistinguishesTheBodyConstraint(t *testing.T) {
	err := classifyCommentError(&pgconn.PgError{Code: pgerrcode.CheckViolation, ConstraintName: "comments_body_check"})

	if !errors.Is(err, ErrCommentBodyInvalid) {
		t.Fatalf("err = %v, want ErrCommentBodyInvalid", err)
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want it to unwrap to ErrInvalidInput", err)
	}
}

func TestClassifyCommentErrorForeignKeyViolationIsNotFound(t *testing.T) {
	err := classifyCommentError(&pgconn.PgError{Code: pgerrcode.ForeignKeyViolation})

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestClassifyCommentErrorOtherCheckViolationsStayGeneric(t *testing.T) {
	err := classifyCommentError(&pgconn.PgError{Code: pgerrcode.CheckViolation, Message: "child comments must reference the root comment for the same review, commit, file, and line"})

	if errors.Is(err, ErrCommentBodyInvalid) {
		t.Fatalf("err = %v, want it not to be classified as the body constraint", err)
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want it to still unwrap to ErrInvalidInput", err)
	}
}
