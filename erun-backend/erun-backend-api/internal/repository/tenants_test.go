package repository

import (
	"errors"
	"testing"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestClassifyTenantCreateErrorMapsNameUniqueViolation(t *testing.T) {
	err := classifyTenantCreateError(&pgconn.PgError{Code: pgerrcode.UniqueViolation, ConstraintName: "tenants_name_key"})

	if !errors.Is(err, ErrTenantNameConflict) {
		t.Fatalf("err = %v, want ErrTenantNameConflict", err)
	}
}

func TestClassifyTenantCreateErrorLeavesOtherViolationsAlone(t *testing.T) {
	err := classifyTenantCreateError(&pgconn.PgError{Code: pgerrcode.UniqueViolation, ConstraintName: "tenants_pkey"})

	if errors.Is(err, ErrTenantNameConflict) {
		t.Fatalf("err = %v, want anything but ErrTenantNameConflict for a different constraint", err)
	}
}
