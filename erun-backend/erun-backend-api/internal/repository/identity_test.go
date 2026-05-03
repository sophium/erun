package repository

import (
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

func TestIdentityRepositoryKeepsPostgresDialect(t *testing.T) {
	repo := NewIdentityRepository(nil, DialectPostgres)
	if repo.dialect != DialectPostgres {
		t.Fatalf("expected postgres dialect, got %q", repo.dialect)
	}
}

func TestTenantTypeValuesRemainDatabaseBacked(t *testing.T) {
	if model.TenantTypeOperations != "OPERATIONS" || model.TenantTypeCompany != "COMPANY" {
		t.Fatalf("unexpected tenant type constants")
	}
}
