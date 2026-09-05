package repository

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestNormalizeNoRowsMapsMalformedInputToErrInvalidInput is the regression
// test for erun#2136: a syntactically malformed id (e.g. "not-a-uuid" against
// a UUID column) produces PostgreSQL SQLSTATE 22P02
// (invalid_text_representation), which every Get-by-id repository method
// routes through normalizeNoRows. Before the fix this fell through
// unclassified and writeRepositoryError's default case reported it as a 500;
// it must report ErrInvalidInput (400) instead, same as any other bad input.
func TestNormalizeNoRowsMapsMalformedInputToErrInvalidInput(t *testing.T) {
	err := normalizeNoRows(&pgconn.PgError{Code: pgerrcode.InvalidTextRepresentation})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("normalizeNoRows(invalid_text_representation) = %v, want ErrInvalidInput", err)
	}
}

// TestNormalizeNoRowsStillMapsNoRowsToErrNotFound guards the existing
// well-formed-but-absent behavior so the new classification above cannot
// collapse the two distinct outcomes into one.
func TestNormalizeNoRowsStillMapsNoRowsToErrNotFound(t *testing.T) {
	err := normalizeNoRows(sql.ErrNoRows)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("normalizeNoRows(sql.ErrNoRows) = %v, want ErrNotFound", err)
	}
}

// TestNormalizeNoRowsPassesThroughOtherErrors guards against over-eager
// classification: a genuine server-side failure (an unrelated PG error, or a
// plain Go error) must still reach the caller unclassified so
// writeRepositoryError's default 500 case still fires for real faults.
func TestNormalizeNoRowsPassesThroughOtherErrors(t *testing.T) {
	other := &pgconn.PgError{Code: pgerrcode.UniqueViolation}
	if got := normalizeNoRows(other); got != other {
		t.Fatalf("normalizeNoRows(unique_violation) = %v, want the error passed through unchanged", got)
	}
	plain := errors.New("boom")
	if got := normalizeNoRows(plain); got != plain {
		t.Fatalf("normalizeNoRows(plain error) = %v, want it passed through unchanged", got)
	}
}
