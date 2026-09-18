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
	// ErrTenantNameConflict signals tenants.name's own UNIQUE constraint
	// specifically, distinguished from the tenant_issuers conflict TenantRepository.Create
	// also guards against: a tenant this call did not create already holds the
	// requested name, so a caller resolving one (e.g. ApproveCreateTenant) can
	// look the existing tenant up instead of treating this as a generic 409.
	ErrTenantNameConflict = errors.New("tenant name already exists")
	// ErrLastGrantCapableRole guards against a tenant locking itself out of its
	// own role management: revoking it would leave no user able to grant roles,
	// and there would be no recovery lever left inside the product.
	ErrLastGrantCapableRole = errors.New("revoking this role would leave the tenant with no user able to grant roles")
	// ErrCommentBodyInvalid signals the comments.body CHECK constraint
	// specifically (empty/whitespace-only or over 8 KiB), distinguished from
	// other check_violations so a caller can be given the documented
	// INVALID_BODY machine code instead of a generic one.
	ErrCommentBodyInvalid = fmt.Errorf("comment body is empty or exceeds 8 KiB: %w", ErrInvalidInput)
	// ErrUsernameConflict signals users_tenant_username_key specifically: a
	// different identity already holds the requested username in the target
	// tenant. Distinguished from ErrIdentityConflict so a caller is told which
	// of the table's two uniqueness contracts actually fired, instead of a
	// single conflict message guessing at the cause.
	ErrUsernameConflict = fmt.Errorf("a user with this username already exists in the target tenant: %w", ErrConflict)
	// ErrUnrecognizedConflict is UserRepository.Create's fallback for a
	// uniqueness violation that matches neither users_tenant_username_key nor
	// user_external_ids' primary key — reported as genuinely unknown rather
	// than guessed at as the most likely-sounding cause.
	ErrUnrecognizedConflict = fmt.Errorf("an unrecognized uniqueness constraint was violated: %w", ErrConflict)
	// ErrIdentityResolutionFailed replaces a raw database error (a constraint
	// name and SQLSTATE, meaningless to an operator or the console rendering
	// it) that IdentityRepository.ResolveIdentity would otherwise return
	// verbatim. The auth middleware logs and returns an identity error's
	// message directly as both the rejection reason and the client-facing
	// response (erun-backend-api's auth.go Wrap), so this sentinel's own safe
	// message is what reaches both; the raw detail is logged server-side by
	// the repository before it is replaced.
	ErrIdentityResolutionFailed = errors.New("identity could not be resolved because of an internal error")
)

// normalizeNoRows maps a lookup query's driver-level failures onto the
// repository's sentinel errors so a caller-supplied path value that is
// syntactically malformed (e.g. "not-a-uuid" against a UUID column) reaches
// the route layer as ErrInvalidInput (400), the same as any other bad input,
// rather than falling through to writeRepositoryError's 500 default. Every
// Get-by-id repository method routes its query error through this so the
// fix applies once, at the layer that first sees the malformed value,
// instead of per route.
func normalizeNoRows(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if code, ok := pgErrorCode(err); ok && code == pgerrcode.InvalidTextRepresentation {
		return ErrInvalidInput
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
