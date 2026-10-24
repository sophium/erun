package repository

import (
	"errors"
	"testing"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestClassifyBuildErrorForeignKeyViolationIsNotFound(t *testing.T) {
	err := classifyBuildError(&pgconn.PgError{Code: pgerrcode.ForeignKeyViolation})

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestClassifyBuildErrorCheckViolationIsInvalidInput(t *testing.T) {
	err := classifyBuildError(&pgconn.PgError{Code: pgerrcode.CheckViolation})

	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}
