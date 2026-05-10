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

func TestDefaultTenantIssuerNameUsesIssuer(t *testing.T) {
	if got := defaultTenantIssuerName(" https://issuer.example "); got != "https://issuer.example" {
		t.Fatalf("unexpected issuer name: %q", got)
	}
}

func TestDefaultTenantIssuerNameFallsBackForEmptyIssuer(t *testing.T) {
	if got := defaultTenantIssuerName(" "); got != "OIDC issuer" {
		t.Fatalf("unexpected empty issuer fallback: %q", got)
	}
}
