package repository

import (
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

func TestIdentityRepositoryKeepsPostgresDialect(t *testing.T) {
	repo := NewIdentityRepository(nil, DialectPostgres, "")
	if repo.dialect != DialectPostgres {
		t.Fatalf("expected postgres dialect, got %q", repo.dialect)
	}
}

func TestIdentityRepositoryTrimsPlatformTenant(t *testing.T) {
	repo := NewIdentityRepository(nil, DialectPostgres, "  frs  ")
	if repo.platformTenant != "frs" {
		t.Fatalf("expected trimmed platform tenant %q, got %q", "frs", repo.platformTenant)
	}
}

func TestBootstrapTenantNameUsesPlatformTenantWhenConfigured(t *testing.T) {
	if got := bootstrapTenantName("frs"); got != "frs" {
		t.Fatalf("expected platform tenant name, got %q", got)
	}
}

func TestBootstrapTenantNameFallsBackWhenPlatformTenantAbsent(t *testing.T) {
	if got := bootstrapTenantName(""); got != defaultBootstrapTenantName {
		t.Fatalf("expected fallback name %q, got %q", defaultBootstrapTenantName, got)
	}
}

func TestBootstrapTenantNameSourceReflectsWhichBranchRan(t *testing.T) {
	if got := bootstrapTenantNameSource("frs"); got != "ERUN_TENANT" {
		t.Fatalf("expected ERUN_TENANT source, got %q", got)
	}
	if got := bootstrapTenantNameSource(""); got != "fallback" {
		t.Fatalf("expected fallback source, got %q", got)
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
