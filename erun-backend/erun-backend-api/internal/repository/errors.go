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
	// ErrTenantNameConflict is tenants.name's UNIQUE violation, so a caller can
	// look up the existing tenant instead of returning a generic 409.
	ErrTenantNameConflict = errors.New("tenant name already exists")
	// ErrLastGrantCapableRole prevents a tenant losing every user able to grant roles.
	ErrLastGrantCapableRole = errors.New("revoking this role would leave the tenant with no user able to grant roles")
	// ErrCommentBodyInvalid maps to the documented INVALID_BODY machine code.
	ErrCommentBodyInvalid = fmt.Errorf("comment body is empty or exceeds 8 KiB: %w", ErrInvalidInput)
	// ErrUsernameConflict is users_tenant_username_key specifically.
	ErrUsernameConflict = fmt.Errorf("a user with this username already exists in the target tenant: %w", ErrConflict)
	// ErrUnrecognizedConflict is a uniqueness violation of no known constraint.
	ErrUnrecognizedConflict = fmt.Errorf("an unrecognized uniqueness constraint was violated: %w", ErrConflict)
	// ErrIdentityResolutionFailed replaces raw database errors in ResolveIdentity,
	// because the auth middleware returns its message to the client verbatim.
	ErrIdentityResolutionFailed = errors.New("identity could not be resolved because of an internal error")
)

// normalizeNoRows maps no-rows to ErrNotFound and malformed input (e.g. a
// non-UUID id) to ErrInvalidInput, so routes return 400 rather than 500.
func normalizeNoRows(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if code, ok := pgErrorCode(err); ok && code == pgerrcode.InvalidTextRepresentation {
		return ErrInvalidInput
	}
	return err
}

// isUniqueViolation reports a PostgreSQL unique violation.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation
}

// isForeignKeyViolation reports a PostgreSQL FK violation; RLS makes another
// tenant's rows look nonexistent, so this also covers cross-tenant references.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrcode.ForeignKeyViolation
}

func pgErrorCode(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return "", false
	}
	return pgErr.Code, true
}

// pgConstraintName returns the violated constraint's name; trigger-raised
// errors carry none.
func pgConstraintName(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName == "" {
		return "", false
	}
	return pgErr.ConstraintName, true
}
