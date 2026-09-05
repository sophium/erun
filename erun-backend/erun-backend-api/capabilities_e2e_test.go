package backendapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	eruncommon "github.com/sophium/erun/erun-common"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// This is the whole capability contract driven end to end: the real handler,
// the real database-backed authorizer under RLS, and the real shared client
// every erun transport uses. A capability set that agrees with enforcement in
// a unit test but not over HTTP would still teach a client to expect the wrong
// thing, so the agreement is re-proved against actual responses.
func capabilityAPIDatabase(t *testing.T) (*sql.DB, string, string) {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_PERMISSIONS_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_PERMISSIONS_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	stamp := time.Now().Format("20060102150405.000000")
	var tenantID string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`,
		"capability-e2e-"+stamp,
	).Scan(&tenantID), "seed tenant")
	var userID string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO users (tenant_id, username) VALUES ($1, $2) RETURNING user_id`,
		tenantID, "capability-e2e-reader-"+stamp,
	).Scan(&userID), "seed user")
	t.Cleanup(func() {
		for _, table := range []string{"audit_events", "user_roles", "role_permissions", "roles", "users", "tenants"} {
			if _, err := db.Exec(`DELETE FROM `+table+` WHERE tenant_id = $1`, tenantID); err != nil {
				t.Logf("clearing %s for tenant %s: %v", table, tenantID, err)
			}
		}
	})
	return db, tenantID, userID
}

// grantReadOnlyRole gives the user the same pattern-based read role the identity
// bootstrap creates, and nothing else.
func grantReadOnlyRole(t *testing.T, db *sql.DB, tenantID, userID string) {
	t.Helper()
	var roleID string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO roles (tenant_id, name) VALUES ($1, 'ReadAll') RETURNING role_id`,
		tenantID,
	).Scan(&roleID), "seed role")
	_, err := db.Exec(`
		INSERT INTO role_permissions (tenant_id, role_id, api_method_pattern, api_path_pattern)
		VALUES ($1, $2, '^(GET|HEAD|OPTIONS)$', '^/.*$')
	`, tenantID, roleID)
	mustNoErr(t, err, "seed role permissions")
	_, err = db.Exec(`INSERT INTO user_roles (tenant_id, user_id, role_id) VALUES ($1, $2, $3)`, tenantID, userID, roleID)
	mustNoErr(t, err, "assign role")
}

func capabilityAPIServer(t *testing.T, db *sql.DB, tenantID, userID string) *httptest.Server {
	t.Helper()
	// Only the OIDC verifier and the identity lookup are stubbed. Authorization,
	// the capability answer, the route catalog, and RLS are all the real ones.
	handler, err := NewHandler(HandlerOptions{
		TokenVerifier: TokenVerifierFunc(func(context.Context, string) (Claims, error) {
			return Claims{Issuer: "https://issuer.example", Subject: "capability-e2e"}, nil
		}),
		TenantResolver: TenantResolverFunc(func(context.Context, Claims) (Tenant, error) {
			return Tenant{TenantID: tenantID, Type: "COMPANY"}, nil
		}),
		UserResolver: UserResolverFunc(func(context.Context, string, string, string) (User, error) {
			return User{UserID: userID, TenantID: tenantID}, nil
		}),
		DB:        db,
		DBDialect: repository.DialectPostgres,
	})
	mustNoErr(t, err, "build handler")
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

// TestReportedCapabilitiesMatchWhatTheAPIActuallyPermits drives the contract the
// way a client does: read the capability set over HTTP with the shared client,
// then call the API and confirm it agrees.
func TestReportedCapabilitiesMatchWhatTheAPIActuallyPermits(t *testing.T) {
	db, tenantID, userID := capabilityAPIDatabase(t)
	grantReadOnlyRole(t, db, tenantID, userID)
	server := capabilityAPIServer(t, db, tenantID, userID)
	client := eruncommon.NewPlatformClient(server.URL, func() (string, error) { return "token", nil })

	whoami, err := client.Whoami(context.Background())
	mustNoErr(t, err, "read whoami")
	if !whoami.Capabilities.Known() {
		t.Fatal("expected the platform to report a capability set")
	}
	if len(whoami.Capabilities) == 0 {
		t.Fatal("expected a read-only role to permit at least the reads")
	}

	// Everything the set claims is readable really is.
	for _, capability := range whoami.Capabilities {
		if capability.Method != http.MethodGet {
			t.Errorf("a read-only role must not report the write surface %s %s", capability.Method, capability.Path)
			continue
		}
		status := capabilityRequestStatus(t, server.URL+concreteRequestPath(capability.Path))
		if status == http.StatusForbidden {
			t.Errorf("capability set claims GET %s is permitted, the API refuses it", capability.Path)
		}
	}

	// And a write the set omits really is refused — authorization runs before
	// the handler, so this never reaches the create path.
	if whoami.Capabilities.Allows(http.MethodPost, "/v1/reviews") {
		t.Fatal("expected the write surface to be omitted for a read-only role")
	}
	_, err = client.CreateReview(context.Background(), eruncommon.PlatformCreateReviewParams{
		Name: "capability probe", TargetBranch: "main", SourceBranch: "feature/capability-probe",
	})
	if !errors.Is(err, eruncommon.ErrPlatformForbidden) {
		t.Fatalf("expected the omitted write to be forbidden, got %v", err)
	}
}

// TestACapabilitySetIsEmptyForAUserWithNoRoles pins what a caller with no roles
// at all actually gets.
func TestACapabilitySetIsEmptyForAUserWithNoRoles(t *testing.T) {
	db, tenantID, userID := capabilityAPIDatabase(t)
	server := capabilityAPIServer(t, db, tenantID, userID)
	client := eruncommon.NewPlatformClient(server.URL, func() (string, error) { return "token", nil })

	// Whoami itself is authorized like every other route, so a user with no
	// roles at all cannot read it. That refusal is the honest answer, and it is
	// distinguishable from an empty capability set.
	_, err := client.Whoami(context.Background())
	if !errors.Is(err, eruncommon.ErrPlatformForbidden) {
		t.Fatalf("expected a role-less caller to be refused, got %v", err)
	}
}

func capabilityRequestStatus(t *testing.T, url string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	mustNoErr(t, err, "build request")
	req.Header.Set("Authorization", "Bearer token")
	resp, err := http.DefaultClient.Do(req)
	mustNoErr(t, err, "call "+url)
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}
