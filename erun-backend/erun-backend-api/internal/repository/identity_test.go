package repository

import (
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
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

// A platform's own IdP serves every tenant from one issuer, so bootstrap has
// to record the claim that discriminates them. Registering it single-tenant
// permanently refuses every later tenant on that issuer, and no API can undo
// it (issue #1605).
func TestBootstrapOrgScopeReadsTheShippedIdPOrgClaim(t *testing.T) {
	claims := security.Claims{Issuer: "https://auth.example.com", Raw: map[string]any{
		"urn:zitadel:iam:user:resourceowner:id":   " 386994597030592700 ",
		"urn:zitadel:iam:user:resourceowner:name": "frs",
	}}
	key, value := bootstrapOrgScope(claims)
	if key != "urn:zitadel:iam:user:resourceowner:id" {
		t.Fatalf("key = %q, want the resourceowner id claim", key)
	}
	if value != "386994597030592700" {
		t.Fatalf("value = %q, want the claim trimmed", value)
	}
}

// An issuer erun does not ship keeps the single-tenant registration: that is
// correct for a dedicated per-tenant IdP, and is what every deployment made
// before this change already has.
func TestBootstrapOrgScopeStaysSingleTenantWithoutAKnownClaim(t *testing.T) {
	for _, raw := range []map[string]any{
		nil,
		{"sub": "user-1"},
		{"urn:zitadel:iam:user:resourceowner:id": ""},
		{"urn:zitadel:iam:user:resourceowner:id": "   "},
		{"urn:zitadel:iam:user:resourceowner:id": 42},
	} {
		key, value := bootstrapOrgScope(security.Claims{Raw: raw})
		if key != "" || value != "" {
			t.Fatalf("claims %v produced scope key=%q value=%q, want single-tenant", raw, key, value)
		}
	}
}
