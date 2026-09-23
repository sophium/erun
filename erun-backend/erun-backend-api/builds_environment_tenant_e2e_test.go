package backendapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// builds_environment_tenant_e2e_test.go proves against a real migrated
// PostgreSQL and a real handler that a build's environment reference is
// scoped to the caller's tenant, and -- the property the whole fix turns on
// -- that the refusal for an environment id belonging to another tenant is
// indistinguishable from the refusal for one that exists nowhere.
//
// The failure this reproduces: builds.environment_id was a single-column
// foreign key to environments.environment_id, so any existing environment id
// -- another tenant's included -- satisfied it, while an id that existed
// nowhere did not. The route took a caller-supplied environment id without
// checking it, and the build row's own tenant_id is the caller's, so
// row-level security never objected. A 201 versus a refusal therefore
// reported whether an id existed in any tenant: an existence oracle over
// every tenant's ids, open to any tenant user. Both halves are exercised
// here -- with the pre-fix code the cross-tenant post is created and the
// unknown one is not, which is exactly the difference this test asserts
// cannot exist.
type buildsEnvironmentTenantE2E struct {
	databaseURL string
}

func buildsEnvironmentTenantE2EFromEnv(t *testing.T) buildsEnvironmentTenantE2E {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_REVIEWS_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_REVIEWS_DATABASE_URL to a migrated PostgreSQL")
	}
	return buildsEnvironmentTenantE2E{databaseURL: databaseURL}
}

// tokenForIssuer lets one handler stand in for two tenants: each token
// resolves to its own issuer/subject pair, which is what the real identity
// resolver keys the tenant on.
func buildsEnvironmentTenantToken(issuer string) string { return "builds-env-tenant-e2e|" + issuer }

func startBuildsEnvironmentTenantE2E(t *testing.T, config buildsEnvironmentTenantE2E, issuers map[string]string) *httptest.Server {
	t.Helper()

	db, err := sql.Open("pgx", config.databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	handler, err := NewHandler(HandlerOptions{
		TokenVerifier: TokenVerifierFunc(func(_ context.Context, token string) (Claims, error) {
			for issuer, subject := range issuers {
				if token == buildsEnvironmentTenantToken(issuer) {
					return Claims{Issuer: issuer, Subject: subject, Username: subject}, nil
				}
			}
			return Claims{}, errors.New("invalid dev token")
		}),
		IdentityCache: NewIdentityResolutionCache(IdentityCacheOptions{}),
		DB:            db,
		DBDialect:     repository.DialectPostgres,
	})
	mustNoErr(t, err, "new handler")

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// seedBuildsEnvironmentTenant registers a tenant that can sign in, and one
// environment of its own to report builds against.
func seedBuildsEnvironmentTenant(t *testing.T, db *sql.DB, namePrefix string) (tenantID, issuer, subject, environmentID string) {
	t.Helper()
	tenantID, issuer = seedTenantWithIssuer(t, db, model.TenantTypeCompany, namePrefix)
	subject = namePrefix + "-subject"
	bootstrapTenantFirstUser(t, db, issuer, subject)

	mustNoErr(t, db.QueryRow(
		`INSERT INTO environments (tenant_id, name, type) VALUES ($1, $2, 'local-agent') RETURNING environment_id`,
		tenantID, namePrefix+"-env",
	).Scan(&environmentID), "seed environment")
	// Cleanup is LIFO, so this runs before seedTenantWithIssuer's own tenant
	// teardown: the builds and environments this test created reference the
	// tenant, and the audited requests it makes leave audit_events behind,
	// which hold a reference to the user.
	t.Cleanup(func() {
		for _, statement := range []string{
			`DELETE FROM builds WHERE tenant_id = $1`,
			`DELETE FROM environments WHERE tenant_id = $1`,
			`DELETE FROM audit_events WHERE tenant_id = $1`,
		} {
			if _, err := db.Exec(statement, tenantID); err != nil {
				t.Logf("clearing %s for tenant %s: %v", statement, tenantID, err)
			}
		}
	})
	return tenantID, issuer, subject, environmentID
}

// postBuildAs reports a build as the tenant behind token and returns the
// response verbatim, so a caller can compare two refusals byte for byte.
func postBuildAs(t *testing.T, baseURL, token, environmentID string) (int, string) {
	t.Helper()
	body := map[string]any{
		"successful": true,
		"commitId":   fmt.Sprintf("%040x", 0xabcdef),
		"version":    "1.0.0",
	}
	if environmentID != "" {
		body["environmentId"] = environmentID
	}
	buf, err := json.Marshal(body)
	mustNoErr(t, err, "marshal body")

	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/builds", bytes.NewReader(buf))
	mustNoErr(t, err, "new request")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	mustNoErr(t, err, "do request")
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	mustNoErr(t, err, "read body")
	return resp.StatusCode, string(out)
}

// TestBuildsCannotTellAnotherTenantsEnvironmentFromAMissingOne is the
// reproduction: with the single-column reference the cross-tenant report was
// created (201) and the unknown one was refused, so the pair told an
// attacker which ids exist in other tenants. After the fix the two answers
// are the same status and the same bytes.
func TestBuildsCannotTellAnotherTenantsEnvironmentFromAMissingOne(t *testing.T) {
	config := buildsEnvironmentTenantE2EFromEnv(t)
	db, err := sql.Open("pgx", config.databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	_, issuerA, subjectA, environmentA := seedBuildsEnvironmentTenant(t, db, "builds-env-tenant-a")
	_, issuerB, subjectB, environmentB := seedBuildsEnvironmentTenant(t, db, "builds-env-tenant-b")

	// A well-formed id that names no environment anywhere.
	const missingEnvironment = "01920000-0000-7000-8000-00000000dead"

	srv := startBuildsEnvironmentTenantE2E(t, config, map[string]string{
		issuerA: subjectA,
		issuerB: subjectB,
	})

	// Control: the caller's own environment is accepted, so a refusal below
	// is about not owning the id rather than about reporting a build at all.
	if code, body := postBuildAs(t, srv.URL, buildsEnvironmentTenantToken(issuerA), environmentA); code != http.StatusCreated {
		t.Fatalf("reporting a build against the caller's own environment: HTTP %d (want 201): %s", code, body)
	}

	crossTenantCode, crossTenantBody := postBuildAs(t, srv.URL, buildsEnvironmentTenantToken(issuerA), environmentB)
	missingCode, missingBody := postBuildAs(t, srv.URL, buildsEnvironmentTenantToken(issuerA), missingEnvironment)

	if missingCode == http.StatusCreated {
		t.Fatalf("a build reported against an environment that exists nowhere was created: HTTP %d: %s", missingCode, missingBody)
	}
	// Both are the ordinary not-found a caller's own RLS-scoped environment
	// read produces, which is what an unknown id already answered before this
	// change -- pinning it keeps a future edit from turning the cross-tenant
	// case into a different status, which would reintroduce the oracle in the
	// other direction.
	if crossTenantCode != http.StatusNotFound || missingCode != http.StatusNotFound {
		t.Fatalf("expected both refusals to be %d, got cross-tenant %d and missing %d\n  other tenant: %s\n  missing:      %s",
			http.StatusNotFound, crossTenantCode, missingCode, crossTenantBody, missingBody)
	}
	if crossTenantCode != missingCode {
		t.Fatalf("another tenant's environment and a missing one answer differently: HTTP %d vs HTTP %d\n  other tenant: %s\n  missing:      %s",
			crossTenantCode, missingCode, crossTenantBody, missingBody)
	}
	if crossTenantBody != missingBody {
		t.Fatalf("the two refusals carry different bodies:\n  other tenant: %q\n  missing:      %q", crossTenantBody, missingBody)
	}
}

// TestBuildsDoNotStoreAnotherTenantsEnvironment is the storage half: even
// behind the route's own refusal, the schema must not let a build row name an
// environment outside its tenant, so a future caller that bypasses the route
// cannot reintroduce the oracle.
func TestBuildsDoNotStoreAnotherTenantsEnvironment(t *testing.T) {
	config := buildsEnvironmentTenantE2EFromEnv(t)
	db, err := sql.Open("pgx", config.databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	tenantAID, _, _, _ := seedBuildsEnvironmentTenant(t, db, "builds-env-store-a")
	_, _, _, environmentB := seedBuildsEnvironmentTenant(t, db, "builds-env-store-b")

	_, err = db.Exec(
		`INSERT INTO builds (tenant_id, environment_id, successful, commit_id, version)
		 VALUES ($1, $2, true, repeat('a', 40), '1.0.0')`,
		tenantAID, environmentB,
	)
	if err == nil {
		_, _ = db.Exec(`DELETE FROM builds WHERE tenant_id = $1 AND environment_id = $2`, tenantAID, environmentB)
		t.Fatal("the schema stored a build naming another tenant's environment")
	}

	// The row that is allowed -- the caller's own environment -- still goes
	// in, so the refusal above is the tenant boundary and not a broken write.
	var own string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO builds (tenant_id, environment_id, successful, commit_id, version)
		 VALUES ($1, (SELECT environment_id FROM environments WHERE tenant_id = $1 LIMIT 1), true, repeat('b', 40), '1.0.0')
		 RETURNING environment_id`,
		tenantAID,
	).Scan(&own), "insert a build naming the caller's own environment")

	// A build with no environment at all is still a real row, not a casualty
	// of making the reference composite.
	var unattached string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO builds (tenant_id, environment_id, successful, commit_id, version)
		 VALUES ($1, NULL, true, repeat('c', 40), '1.0.0') RETURNING build_id`,
		tenantAID,
	).Scan(&unattached), "insert a build with no environment")
}
