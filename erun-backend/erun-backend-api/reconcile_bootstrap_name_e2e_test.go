package backendapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/testpg"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// The rename must be proven on persisted rows, so this reads the database back.
func reconcileBootstrapNameDatabase(t *testing.T) *sql.DB {
	t.Helper()
	databaseURL := testpg.URL(t)
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })
	return db
}

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

// The first user goes through the real bootstrap path, which grants ReadAll+WriteAll;
// a roleless caller would fail for the wrong reason.
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
	return resp
}

func TestReconcileBootstrapNameAppliesWhenTenantHasNoEnvironments(t *testing.T) {
	db := reconcileBootstrapNameDatabase(t)
	suffix := "-" + time.Now().Format("20060102150405.000000")
	legacyName := "operations-apply" + suffix
	declaredName := "frs-apply" + suffix
	issuer := "https://issuer.example/apply" + suffix
	tenantID := seedLegacyOperationsTenant(t, db, legacyName, issuer, "operator-subject")

	srv := startReconcileAPI(t, db, declaredName, issuer, "operator-subject")
	resp := patchReconcileBootstrapName(t, srv)
	defer func() { _ = resp.Body.Close() }()
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

func TestReconcileBootstrapNameRefusesWhenTenantHasEnvironments(t *testing.T) {
	db := reconcileBootstrapNameDatabase(t)
	suffix := "-" + time.Now().Format("20060102150405.000000")
	legacyName := "operations-refuse" + suffix
	declaredName := "frs-refuse" + suffix
	issuer := "https://issuer.example/refuse" + suffix
	tenantID := seedLegacyOperationsTenant(t, db, legacyName, issuer, "operator-subject")
	// Seeded through the repository so it runs under erun_operations RLS, not the test
	// connection's bypass.
	envCtx := security.WithContext(context.Background(), security.Context{TenantID: tenantID, TenantType: "OPERATIONS"})
	envRepo := repository.NewEnvironmentRepository(repository.NewTxManager(db, repository.DialectPostgres))
	_, err := envRepo.Create(envCtx, model.Environment{Name: "existing-env", Type: model.EnvironmentTypeRuntime})
	mustNoErr(t, err, "seed environment")

	srv := startReconcileAPI(t, db, declaredName, issuer, "operator-subject")
	resp := patchReconcileBootstrapName(t, srv)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}

	if got := tenantNameByID(t, db, tenantID); got != legacyName {
		t.Fatalf("persisted tenant name = %q, want unchanged %q", got, legacyName)
	}
}

func TestReconcileBootstrapNameForbidsNonOperationsCaller(t *testing.T) {
	db := reconcileBootstrapNameDatabase(t)
	suffix := "-" + time.Now().Format("20060102150405.000000")
	// Empty-database bootstrap runs once, so the OPERATIONS tenant must exist first.
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
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if got := tenantNameByID(t, db, companyTenantID); got != companyName {
		t.Fatalf("persisted company tenant name = %q, want unchanged %q", got, companyName)
	}
	_ = opsTenantID
}
