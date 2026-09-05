package repository

import (
	"bytes"
	"errors"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
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

// TestSanitizeResolutionErrorReplacesRawPostgresErrors proves erun#1752's
// item 3: a raw PostgreSQL error (a constraint name and SQLSTATE) must never
// escape ResolveIdentity, since the auth middleware logs and returns an
// identity error's message verbatim as both the auth-rejected reason and the
// client-facing response. The step label distinguishes which internal branch
// produced it (item 2) in the repository's own log line -- not by repeating
// the raw driver detail, which stays server-side only as a fixed, safe
// phrase.
func TestSanitizeResolutionErrorReplacesRawPostgresErrors(t *testing.T) {
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	pgErr := &pgconn.PgError{
		Code:           pgerrcode.UniqueViolation,
		ConstraintName: "users_tenant_username_key",
		Message:        "duplicate key value violates unique constraint \"users_tenant_username_key\"",
	}
	claims := security.Claims{Issuer: "https://auth.erunpaas.com", Subject: "387534471668170904"}

	got := sanitizeResolutionError(pgErr, "bootstrap first tenant user", "tenant-1", claims)

	if !errors.Is(got, ErrIdentityResolutionFailed) {
		t.Fatalf("got = %v, want ErrIdentityResolutionFailed", got)
	}
	logged := logBuf.String()
	if strings.Contains(logged, "SQLSTATE") || strings.Contains(logged, "users_tenant_username_key") || strings.Contains(logged, "duplicate key") {
		t.Fatalf("log output leaked raw Postgres detail: %q", logged)
	}
	if !strings.Contains(logged, `step="bootstrap first tenant user"`) {
		t.Fatalf("log output = %q, want the step label naming which branch failed", logged)
	}
}

// TestSanitizeResolutionErrorLeavesSafeOutcomesUnchanged proves the two
// expected, already-classified outcomes (ErrNotFound and
// security.ErrTenantUnresolved) pass through untouched -- sanitization must
// only ever intercept a raw database error, never override the not-enrolled
// or tenant-unresolved decisions the rest of ResolveIdentity already made.
func TestSanitizeResolutionErrorLeavesSafeOutcomesUnchanged(t *testing.T) {
	claims := security.Claims{Issuer: "https://issuer.example", Subject: "user-1"}

	if got := sanitizeResolutionError(ErrNotFound, "user lookup", "tenant-1", claims); !errors.Is(got, ErrNotFound) {
		t.Fatalf("got = %v, want ErrNotFound unchanged", got)
	}
	unresolved := errors.New("issuer unknown")
	if got := sanitizeResolutionError(security.ErrTenantUnresolved, "tenant lookup", "", claims); !errors.Is(got, security.ErrTenantUnresolved) {
		t.Fatalf("got = %v, want security.ErrTenantUnresolved unchanged", got)
	}
	if got := sanitizeResolutionError(unresolved, "tenant lookup", "", claims); errors.Is(got, ErrIdentityResolutionFailed) {
		t.Fatalf("got = %v, a non-Postgres error must not be replaced", got)
	}
}

// A platform's own IdP serves every tenant from one issuer, so bootstrap has
// to record the claim that discriminates them. Registering it single-tenant
// permanently refuses every later tenant on that issuer, and no API can undo
// it.
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
