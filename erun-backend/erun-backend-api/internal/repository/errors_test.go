package repository

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestNormalizeNoRowsMapsMalformedInputToErrInvalidInput(t *testing.T) {
	err := normalizeNoRows(&pgconn.PgError{Code: pgerrcode.InvalidTextRepresentation})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("normalizeNoRows(invalid_text_representation) = %v, want ErrInvalidInput", err)
	}
}

func TestNormalizeNoRowsStillMapsNoRowsToErrNotFound(t *testing.T) {
	err := normalizeNoRows(sql.ErrNoRows)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("normalizeNoRows(sql.ErrNoRows) = %v, want ErrNotFound", err)
	}
}

func TestNormalizeNoRowsPassesThroughOtherErrors(t *testing.T) {
	other := &pgconn.PgError{Code: pgerrcode.UniqueViolation}
	if got := normalizeNoRows(other); !errors.Is(got, other) {
		t.Fatalf("normalizeNoRows(unique_violation) = %v, want the error passed through unchanged", got)
	}
	plain := errors.New("boom")
	if got := normalizeNoRows(plain); !errors.Is(got, plain) {
		t.Fatalf("normalizeNoRows(plain error) = %v, want it passed through unchanged", got)
	}
}
