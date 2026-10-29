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
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/testpg"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// A build's environment reference must be tenant-scoped, and a cross-tenant id
// must be refused exactly like a nonexistent one, or the API is an existence oracle.
type buildsEnvironmentTenantE2E struct {
	databaseURL string
}

func buildsEnvironmentTenantE2EFromEnv(t *testing.T) buildsEnvironmentTenantE2E {
	t.Helper()
	databaseURL := testpg.URL(t)
	return buildsEnvironmentTenantE2E{databaseURL: databaseURL}
}

// Each token resolves to its own issuer, which is what the identity resolver keys the tenant on.
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

func seedBuildsEnvironmentTenant(t *testing.T, db *sql.DB, namePrefix string) (tenantID, issuer, subject, environmentID string) {
	t.Helper()
	tenantID, issuer = seedTenantWithIssuer(t, db, model.TenantTypeCompany, namePrefix)
	subject = namePrefix + "-subject"
	bootstrapTenantFirstUser(t, db, issuer, subject)

	mustNoErr(t, db.QueryRow(
		`INSERT INTO environments (tenant_id, name, type) VALUES ($1, $2, 'local-agent') RETURNING environment_id`,
		tenantID, namePrefix+"-env",
	).Scan(&environmentID), "seed environment")
	// Runs before seedTenantWithIssuer's teardown (LIFO): builds, environments, and
	// audit_events reference the tenant and user.
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

// postBuildAs returns the response verbatim so refusals can be compared byte for byte.
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

func TestBuildsCannotTellAnotherTenantsEnvironmentFromAMissingOne(t *testing.T) {
	config := buildsEnvironmentTenantE2EFromEnv(t)
	db, err := sql.Open("pgx", config.databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	_, issuerA, subjectA, environmentA := seedBuildsEnvironmentTenant(t, db, "builds-env-tenant-a")
	_, issuerB, subjectB, environmentB := seedBuildsEnvironmentTenant(t, db, "builds-env-tenant-b")

	const missingEnvironment = "01920000-0000-7000-8000-00000000dead"

	srv := startBuildsEnvironmentTenantE2E(t, config, map[string]string{
		issuerA: subjectA,
		issuerB: subjectB,
	})

	if code, body := postBuildAs(t, srv.URL, buildsEnvironmentTenantToken(issuerA), environmentA); code != http.StatusCreated {
		t.Fatalf("reporting a build against the caller's own environment: HTTP %d (want 201): %s", code, body)
	}

	crossTenantCode, crossTenantBody := postBuildAs(t, srv.URL, buildsEnvironmentTenantToken(issuerA), environmentB)
	missingCode, missingBody := postBuildAs(t, srv.URL, buildsEnvironmentTenantToken(issuerA), missingEnvironment)

	if missingCode == http.StatusCreated {
		t.Fatalf("a build reported against an environment that exists nowhere was created: HTTP %d: %s", missingCode, missingBody)
	}
	// Both must be the plain not-found; any difference reintroduces the oracle.
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

// The schema itself must refuse a cross-tenant environment, even bypassing the route.
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

	var own string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO builds (tenant_id, environment_id, successful, commit_id, version)
		 VALUES ($1, (SELECT environment_id FROM environments WHERE tenant_id = $1 LIMIT 1), true, repeat('b', 40), '1.0.0')
		 RETURNING environment_id`,
		tenantAID,
	).Scan(&own), "insert a build naming the caller's own environment")

	var unattached string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO builds (tenant_id, environment_id, successful, commit_id, version)
		 VALUES ($1, NULL, true, repeat('c', 40), '1.0.0') RETURNING build_id`,
		tenantAID,
	).Scan(&unattached), "insert a build with no environment")
}
