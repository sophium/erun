package repository

import (
	"errors"
	"testing"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestClassifyBuildErrorForeignKeyViolationIsNotFound: a reviewId the
// caller's tenant cannot see fails the same foreign key check whether the row
// genuinely doesn't exist or just isn't this tenant's, matching the "doesn't
// exist or isn't visible" 404 documented in collaboration/builds.md.
func TestClassifyBuildErrorForeignKeyViolationIsNotFound(t *testing.T) {
	err := classifyBuildError(&pgconn.PgError{Code: pgerrcode.ForeignKeyViolation})

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestClassifyBuildErrorCheckViolationIsInvalidInput covers the remaining
// CHECK constraints on builds (kind, commit_id, version, failure_detail).
func TestClassifyBuildErrorCheckViolationIsInvalidInput(t *testing.T) {
	err := classifyBuildError(&pgconn.PgError{Code: pgerrcode.CheckViolation})

	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}
