package repository

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrForbidden              = errors.New("forbidden")
	ErrInvalidInput           = errors.New("invalid input")
	ErrNotFound               = errors.New("not found")
	ErrMissingSecurityContext = errors.New("missing security context")
	ErrConflict               = errors.New("conflict")
	// ErrLastGrantCapableRole guards against a tenant locking itself out of its
	// own role management: revoking it would leave no user able to grant roles,
	// and there would be no recovery lever left inside the product.
	ErrLastGrantCapableRole = errors.New("revoking this role would leave the tenant with no user able to grant roles")
	// ErrCommentBodyInvalid signals the comments.body CHECK constraint
	// specifically (empty/whitespace-only or over 8 KiB), distinguished from
	// other check_violations so a caller can be given the documented
	// INVALID_BODY machine code instead of a generic one.
	ErrCommentBodyInvalid = fmt.Errorf("comment body is empty or exceeds 8 KiB: %w", ErrInvalidInput)
)

func normalizeNoRows(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// isUniqueViolation reports whether PostgreSQL rejected a write because it would
// have broken a uniqueness contract. Callers that hold an invariant in a unique
// index — at most one release in flight per tenant — read the conflict as "the
// other writer won", not as an error to surface.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation
}

// isForeignKeyViolation reports whether PostgreSQL rejected a write because a
// referenced row does not exist for the caller's own tenant — e.g. a role_id
// or user_id that belongs to a different tenant, which RLS makes invisible
// rather than merely forbidden.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrcode.ForeignKeyViolation
}

// pgErrorCode returns the PostgreSQL SQLSTATE of err, if err wraps one.
func pgErrorCode(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return "", false
	}
	return pgErr.Code, true
}

// pgConstraintName returns the name of the table CHECK/UNIQUE/FK constraint
// err violated, if any. Trigger RAISE EXCEPTIONs (used for cross-row
// invariants Postgres constraints can't express) carry no constraint name —
// this only identifies violations of a real named constraint on the table.
func pgConstraintName(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName == "" {
		return "", false
	}
	return pgErr.ConstraintName, true
}
