package backendapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// PATCH /v1/tenants/reconcile-bootstrap-name (erun#1480) against a real migrated
// PostgreSQL: the rename must be provable on persisted rows, not on a trace,
// which is why this is an HTTP-level gate rather than a fake-backed unit
// test — it drives the real repository/service wiring and then reads the
// database back directly.
func reconcileBootstrapNameDatabase(t *testing.T) *sql.DB {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_TENANTS_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_TENANTS_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// startReconcileAPI wires the API with a declared platform tenant name (the
// ERUN_TENANT this instance would have) and a token verifier that resolves
// exactly one issuer/subject pair, so each test authenticates as one
// specific, already-bootstrapped tenant.
func startReconcileAPI(t *testing.T, db *sql.DB, platformTenant, issuer, subject string) *httptest.Server {
	t.Helper()
	handler, err := NewHandler(HandlerOptions{
		TokenVerifier: TokenVerifierFunc(func(_ context.Context, token string) (Claims, error) {
			if token != e2eDevToken {
				return Claims{}, errors.New("invalid dev token")
			}
			return Claims{Issuer: issuer, Subject: subject, Username: "dev"}, nil
		}),
		IdentityCache:       NewIdentityResolutionCache(IdentityCacheOptions{}),
		DB:                  db,
		DBDialect:           repository.DialectPostgres,
		BootstrapTenantName: platformTenant,
	})
	mustNoErr(t, err, "new handler")
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// seedLegacyOperationsTenant seeds an OPERATIONS tenant under name (standing
// in for a platform bootstrapped before its ERUN_TENANT was read, or under a
// different one) and its issuer mapping, with zero users. bootstrapFirstUser
// then enrols the tenant's first user through the real per-tenant-first-user
// bootstrap path (not a hand-rolled row), which is what actually grants
// ReadAll+WriteAll — a caller with no roles would be forbidden from every
// route, including this one, which is a different failure than the ones
// these tests exist to prove.
func seedLegacyOperationsTenant(t *testing.T, db *sql.DB, name, issuer, subject string) string {
	t.Helper()
	ctx := context.Background()
	var tenantID string
	mustNoErr(t, db.QueryRowContext(ctx,
		`INSERT INTO tenants (name, type) VALUES ($1, 'OPERATIONS') RETURNING tenant_id`, name,
	).Scan(&tenantID), "seed operations tenant")
	_, err := db.ExecContext(ctx, `INSERT INTO issuers (issuer) VALUES ($1) ON CONFLICT (issuer) DO NOTHING`, issuer)
	mustNoErr(t, err, "seed issuer")
	_, err = db.ExecContext(ctx,
		`INSERT INTO tenant_issuers (tenant_id, issuer, name) VALUES ($1, $2, $3)`, tenantID, issuer, name)
	mustNoErr(t, err, "seed tenant_issuers")
	t.Cleanup(func() { clearReconcileTenant(t, db, tenantID) })
	bootstrapFirstUser(t, db, issuer, subject)
	return tenantID
}

// bootstrapFirstUser enrols the first user for an already-registered tenant
// (found by issuer, with zero existing users) through the real identity
// repository, granting the same ReadAll+WriteAll shape empty-database
// bootstrap grants (see erun-backend-api/AGENTS.md's Authentication section).
func bootstrapFirstUser(t *testing.T, db *sql.DB, issuer, subject string) {
	t.Helper()
	repo := repository.NewIdentityRepository(db, repository.DialectPostgres, "")
	_, _, err := repo.ResolveIdentity(context.Background(), security.Claims{Issuer: issuer, Subject: subject, Username: "dev"})
	mustNoErr(t, err, "bootstrap first user for issuer "+issuer)
}

func clearReconcileTenant(t *testing.T, db *sql.DB, tenantID string) {
	t.Helper()
	for _, table := range []string{"environments", "user_external_ids", "users", "tenant_issuers", "tenants"} {
		if _, err := db.Exec(`DELETE FROM `+table+` WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing %s for tenant %s: %v", table, tenantID, err)
		}
	}
}

func tenantNameByID(t *testing.T, db *sql.DB, tenantID string) string {
	t.Helper()
	var name string
	mustNoErr(t, db.QueryRow(`SELECT name FROM tenants WHERE tenant_id = $1`, tenantID).Scan(&name), "read tenant name")
	return name
}

func patchReconcileBootstrapName(t *testing.T, srv *httptest.Server) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPatch, srv.URL+"/v1/tenants/reconcile-bootstrap-name", nil)
	mustNoErr(t, err, "build request")
	req.Header.Set("Authorization", "Bearer "+e2eDevToken)
	resp, err := srv.Client().Do(req)
	mustNoErr(t, err, "do request")
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// TestReconcileBootstrapNameAppliesWhenTenantHasNoEnvironments proves the
// rename against a persisted row: a legacy-named OPERATIONS tenant with zero
// environments really ends up renamed in the tenants table, not merely
// reported as renamed.
func TestReconcileBootstrapNameAppliesWhenTenantHasNoEnvironments(t *testing.T) {
	db := reconcileBootstrapNameDatabase(t)
	suffix := "-" + time.Now().Format("20060102150405.000000")
	legacyName := "operations-apply" + suffix
	declaredName := "frs-apply" + suffix
	issuer := "https://issuer.example/apply" + suffix
	tenantID := seedLegacyOperationsTenant(t, db, legacyName, issuer, "operator-subject")

	srv := startReconcileAPI(t, db, declaredName, issuer, "operator-subject")
	resp := patchReconcileBootstrapName(t, srv)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Name string `json:"name"`
	}
	mustNoErr(t, json.NewDecoder(resp.Body).Decode(&body), "decode response")
	if body.Name != declaredName {
		t.Fatalf("response tenant name = %q, want %q", body.Name, declaredName)
	}

	if got := tenantNameByID(t, db, tenantID); got != declaredName {
		t.Fatalf("persisted tenant name = %q, want %q", got, declaredName)
	}
}

// TestReconcileBootstrapNameRefusesWhenTenantHasEnvironments proves the
// refusal against a persisted row: a legacy-named OPERATIONS tenant with one
// existing environment keeps its original name in the tenants table after a
// refused reconcile call.
func TestReconcileBootstrapNameRefusesWhenTenantHasEnvironments(t *testing.T) {
	db := reconcileBootstrapNameDatabase(t)
	suffix := "-" + time.Now().Format("20060102150405.000000")
	legacyName := "operations-refuse" + suffix
	declaredName := "frs-refuse" + suffix
	issuer := "https://issuer.example/refuse" + suffix
	tenantID := seedLegacyOperationsTenant(t, db, legacyName, issuer, "operator-subject")
	// Seeded through the real repository (not a raw INSERT) so it runs under
	// the same erun_operations RLS role the API itself uses, rather than
	// depending on the test connection's own bypass-RLS privileges.
	envCtx := security.WithContext(context.Background(), security.Context{TenantID: tenantID, TenantType: "OPERATIONS"})
	envRepo := repository.NewEnvironmentRepository(repository.NewTxManager(db, repository.DialectPostgres))
	_, err := envRepo.Create(envCtx, model.Environment{Name: "existing-env", Type: model.EnvironmentTypeRuntime})
	mustNoErr(t, err, "seed environment")

	srv := startReconcileAPI(t, db, declaredName, issuer, "operator-subject")
	resp := patchReconcileBootstrapName(t, srv)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}

	if got := tenantNameByID(t, db, tenantID); got != legacyName {
		t.Fatalf("persisted tenant name = %q, want unchanged %q", got, legacyName)
	}
}

// TestReconcileBootstrapNameForbidsNonOperationsCaller proves the
// authorization boundary end to end: a COMPANY tenant's own caller is
// refused, and its tenant row is left untouched.
func TestReconcileBootstrapNameForbidsNonOperationsCaller(t *testing.T) {
	db := reconcileBootstrapNameDatabase(t)
	suffix := "-" + time.Now().Format("20060102150405.000000")
	// Bootstrap the platform's own OPERATIONS tenant first (empty-database
	// bootstrap only ever runs once), then register an ordinary COMPANY
	// tenant with its own issuer and first user.
	opsIssuer := "https://issuer.example/ops" + suffix
	opsTenantID := seedLegacyOperationsTenant(t, db, "operations-forbid"+suffix, opsIssuer, "ops-subject")

	companyName := "acme-forbid" + suffix
	companyIssuer := "https://issuer.example/company" + suffix
	var companyTenantID string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`, companyName,
	).Scan(&companyTenantID), "seed company tenant")
	t.Cleanup(func() { clearReconcileTenant(t, db, companyTenantID) })
	_, err := db.Exec(`INSERT INTO issuers (issuer) VALUES ($1) ON CONFLICT (issuer) DO NOTHING`, companyIssuer)
	mustNoErr(t, err, "seed company issuer")
	_, err = db.Exec(`INSERT INTO tenant_issuers (tenant_id, issuer, name) VALUES ($1, $2, $3)`, companyTenantID, companyIssuer, companyName)
	mustNoErr(t, err, "seed company tenant_issuers")
	bootstrapFirstUser(t, db, companyIssuer, "company-subject")

	srv := startReconcileAPI(t, db, "frs-forbid"+suffix, companyIssuer, "company-subject")
	resp := patchReconcileBootstrapName(t, srv)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if got := tenantNameByID(t, db, companyTenantID); got != companyName {
		t.Fatalf("persisted company tenant name = %q, want unchanged %q", got, companyName)
	}
	// opsTenantID is never renamed by this scenario -- it exists only so the
	// company tenant above is not the database's first tenant (which would
	// instead exercise empty-database bootstrap for the company issuer).
	_ = opsTenantID
}
