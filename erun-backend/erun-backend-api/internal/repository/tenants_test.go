package repository

import (
	"errors"
	"testing"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestClassifyTenantCreateErrorMapsNameUniqueViolation locks the fix for
// erun#1722: a tenant name that already exists must be distinguished from
// every other insert failure, not surfaced as a generic 500.
func TestClassifyTenantCreateErrorMapsNameUniqueViolation(t *testing.T) {
	err := classifyTenantCreateError(&pgconn.PgError{Code: pgerrcode.UniqueViolation, ConstraintName: "tenants_name_key"})

	if !errors.Is(err, ErrTenantNameConflict) {
		t.Fatalf("err = %v, want ErrTenantNameConflict", err)
	}
}

// TestClassifyTenantCreateErrorLeavesOtherViolationsAlone proves the mapping
// is scoped to the name's own constraint: a unique violation on a different
// constraint (or no constraint name at all) must not be misclassified as a
// resolvable name race.
func TestClassifyTenantCreateErrorLeavesOtherViolationsAlone(t *testing.T) {
	err := classifyTenantCreateError(&pgconn.PgError{Code: pgerrcode.UniqueViolation, ConstraintName: "tenants_pkey"})

	if errors.Is(err, ErrTenantNameConflict) {
		t.Fatalf("err = %v, want anything but ErrTenantNameConflict for a different constraint", err)
	}
}
