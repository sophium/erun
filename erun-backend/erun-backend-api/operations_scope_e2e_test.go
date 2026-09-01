package backendapi

import (
	"context"
	"database/sql"
	"encoding/json"
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

// operationsScopeDatabase seeds one OPERATIONS tenant (the caller) and one
// COMPANY tenant (a stranger) against a real migrated PostgreSQL, so
// EnvironmentRepository/TenantQuotaRepository's tenant scoping is proven
// against the actual erun_operations RLS policy (USING (true)) rather than a
// fake that would just agree with itself about which rows it decided to
// return. Reuses ERUN_E2E_ENVIRONMENT_DATABASE_URL: same tables, same
// tenant_quotas dependency the environment quota gate reads.
func operationsScopeDatabase(t *testing.T) (opsCtx, strangerCtx context.Context, opsTenantID, strangerTenantID string, db *sql.DB) {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_ENVIRONMENT_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_ENVIRONMENT_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	opsTenantID = seedScopeTestTenant(t, db, "ops-scope-e2e", model.TenantTypeOperations)
	strangerTenantID = seedScopeTestTenant(t, db, "stranger-scope-e2e", model.TenantTypeCompany)
	t.Cleanup(func() {
		for _, tenantID := range []string{opsTenantID, strangerTenantID} {
			for _, table := range []string{"audit_events", "usage_events", "user_roles", "role_permissions", "roles", "users", "environments", "contexts", "tenant_quotas", "tenants"} {
				if _, err := db.Exec(`DELETE FROM `+table+` WHERE tenant_id = $1`, tenantID); err != nil {
					t.Logf("clearing %s for tenant %s: %v", table, tenantID, err)
				}
			}
		}
	})

	opsCtx = security.WithContext(context.Background(), security.Context{TenantID: opsTenantID, TenantType: string(model.TenantTypeOperations)})
	strangerCtx = security.WithContext(context.Background(), security.Context{TenantID: strangerTenantID, TenantType: string(model.TenantTypeCompany)})
	return opsCtx, strangerCtx, opsTenantID, strangerTenantID, db
}

func seedScopeTestTenant(t *testing.T, db *sql.DB, label string, tenantType model.TenantType) string {
	t.Helper()
	var tenantID string
	err := db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, $2) RETURNING tenant_id`,
		label+"-"+time.Now().Format("20060102150405.000000"), string(tenantType),
	).Scan(&tenantID)
	mustNoErr(t, err, "seed tenant "+label)
	return tenantID
}

// TestEnvironmentCountScopesToTheOperationsCallersOwnTenant pins the failure
// scenario directly: an OPERATIONS tenant's environment-count quota must
// count only its own environments, never a stranger tenant's, even though
// erun_operations' RLS policy makes the stranger's rows visible too.
func TestEnvironmentCountScopesToTheOperationsCallersOwnTenant(t *testing.T) {
	opsCtx, strangerCtx, _, _, db := operationsScopeDatabase(t)
	environments := repository.NewEnvironmentRepository(repository.NewTxManager(db, repository.DialectPostgres))

	_, err := environments.Create(strangerCtx, model.Environment{Name: "stranger-env-1", Type: model.EnvironmentTypeRuntime, RuntimeVersion: "1.0.0"})
	mustNoErr(t, err, "create stranger environment 1")
	_, err = environments.Create(strangerCtx, model.Environment{Name: "stranger-env-2", Type: model.EnvironmentTypeRuntime, RuntimeVersion: "1.0.0"})
	mustNoErr(t, err, "create stranger environment 2")
	own, err := environments.Create(opsCtx, model.Environment{Name: "ops-env-1", Type: model.EnvironmentTypeRuntime, RuntimeVersion: "1.0.0"})
	mustNoErr(t, err, "create ops environment")

	count, err := environments.Count(opsCtx)
	mustNoErr(t, err, "count as operations caller")
	if count != 1 {
		t.Fatalf("Count = %d, want 1 (the operations caller's own environment, not the stranger's 2 as well)", count)
	}

	list, err := environments.List(opsCtx)
	mustNoErr(t, err, "list as operations caller")
	if len(list) != 1 || list[0].EnvironmentID != own.EnvironmentID {
		t.Fatalf("List = %v, want exactly [%s]", list, own.EnvironmentID)
	}
}

// TestEnvironmentCountByTypeScopesToTheOperationsCallersOwnTenant pins the
// same property for the resource-budget check's per-type count.
func TestEnvironmentCountByTypeScopesToTheOperationsCallersOwnTenant(t *testing.T) {
	opsCtx, strangerCtx, _, _, db := operationsScopeDatabase(t)
	environments := repository.NewEnvironmentRepository(repository.NewTxManager(db, repository.DialectPostgres))

	_, err := environments.Create(strangerCtx, model.Environment{Name: "stranger-runtime", Type: model.EnvironmentTypeRuntime, RuntimeVersion: "1.0.0"})
	mustNoErr(t, err, "create stranger runtime environment")
	_, err = environments.Create(opsCtx, model.Environment{Name: "ops-runtime", Type: model.EnvironmentTypeRuntime, RuntimeVersion: "1.0.0"})
	mustNoErr(t, err, "create ops runtime environment")

	count, err := environments.CountByType(opsCtx, model.EnvironmentTypeRuntime)
	mustNoErr(t, err, "count by type as operations caller")
	if count != 1 {
		t.Fatalf("CountByType = %d, want 1 (the operations caller's own runtime environment, not the stranger's as well)", count)
	}
}

// TestEnvironmentCountByContextScopesToTheOperationsCallersOwnTenant pins the
// placement-capacity check: a stranger's environment placed on the
// stranger's own context must not inflate the operations caller's count for
// that context, and the operations caller's own context/environment must
// still be counted.
func TestEnvironmentCountByContextScopesToTheOperationsCallersOwnTenant(t *testing.T) {
	opsCtx, strangerCtx, _, _, db := operationsScopeDatabase(t)
	txs := repository.NewTxManager(db, repository.DialectPostgres)
	environments := repository.NewEnvironmentRepository(txs)
	contexts := repository.NewContextRepository(txs)

	strangerContext, err := contexts.Create(strangerCtx, model.Context{Name: "stranger-context", Provider: "aws"})
	mustNoErr(t, err, "create stranger context")
	_, err = environments.Create(strangerCtx, model.Environment{Name: "stranger-placed", Type: model.EnvironmentTypeRuntime, RuntimeVersion: "1.0.0", ContextID: strangerContext.ContextID})
	mustNoErr(t, err, "create stranger environment placed on stranger context")

	opsContext, err := contexts.Create(opsCtx, model.Context{Name: "ops-context", Provider: "aws"})
	mustNoErr(t, err, "create ops context")
	_, err = environments.Create(opsCtx, model.Environment{Name: "ops-placed", Type: model.EnvironmentTypeRuntime, RuntimeVersion: "1.0.0", ContextID: opsContext.ContextID})
	mustNoErr(t, err, "create ops environment placed on ops context")

	strangerContextCount, err := environments.CountByContext(opsCtx, strangerContext.ContextID)
	mustNoErr(t, err, "count by stranger's context as operations caller")
	if strangerContextCount != 0 {
		t.Fatalf("CountByContext(strangerContext) = %d, want 0 — the operations caller placed nothing there", strangerContextCount)
	}

	opsContextCount, err := environments.CountByContext(opsCtx, opsContext.ContextID)
	mustNoErr(t, err, "count by own context as operations caller")
	if opsContextCount != 1 {
		t.Fatalf("CountByContext(opsContext) = %d, want 1", opsContextCount)
	}
}

// TestTenantQuotaGetReturnsTheOperationsCallersOwnRow pins the other half of
// the quota gate: reading the cap itself must not return an arbitrary
// tenant's row (or the stranger's) just because erun_operations can see both.
func TestTenantQuotaGetReturnsTheOperationsCallersOwnRow(t *testing.T) {
	opsCtx, _, opsTenantID, strangerTenantID, db := operationsScopeDatabase(t)
	quotas := repository.NewTenantQuotaRepository(repository.NewTxManager(db, repository.DialectPostgres))

	_, err := quotas.Set(opsCtx, strangerTenantID, model.TenantQuota{MaxEnvironments: 99})
	mustNoErr(t, err, "set stranger quota")
	_, err = quotas.Set(opsCtx, opsTenantID, model.TenantQuota{MaxEnvironments: 3})
	mustNoErr(t, err, "set ops quota")

	got, err := quotas.Get(opsCtx)
	mustNoErr(t, err, "get as operations caller")
	if got.MaxEnvironments != 3 {
		t.Fatalf("Get().MaxEnvironments = %d, want 3 (the operations caller's own row, not the stranger's 99)", got.MaxEnvironments)
	}
}

// grantAllAccessRole gives the user a pattern-based role matching every
// method and path, the same shape ReadAll/WriteAll grant, so the HTTP-level
// test below exercises quota enforcement itself rather than authorization.
func grantAllAccessRole(t *testing.T, db *sql.DB, tenantID, userID string) {
	t.Helper()
	var roleID string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO roles (tenant_id, name) VALUES ($1, 'AllAccess') RETURNING role_id`,
		tenantID,
	).Scan(&roleID), "seed role")
	_, err := db.Exec(`
		INSERT INTO role_permissions (tenant_id, role_id, api_method_pattern, api_path_pattern)
		VALUES ($1, $2, '^.*$', '^.*$')
	`, tenantID, roleID)
	mustNoErr(t, err, "seed role permissions")
	_, err = db.Exec(`INSERT INTO user_roles (tenant_id, user_id, role_id) VALUES ($1, $2, $3)`, tenantID, userID, roleID)
	mustNoErr(t, err, "assign role")
}

// startEnvironmentsAPIServer builds a real handler fixed to one caller
// identity (only the OIDC verifier and identity lookup are stubbed;
// authorization, quota enforcement, and RLS are all the real ones), so two
// servers pointed at the same database stand in for two different tenants
// calling the platform.
func startEnvironmentsAPIServer(t *testing.T, db *sql.DB, tenantID string, tenantType model.TenantType, userID string) *httptest.Server {
	t.Helper()
	handler, err := NewHandler(HandlerOptions{
		TokenVerifier: TokenVerifierFunc(func(context.Context, string) (Claims, error) {
			return Claims{Issuer: "https://issuer.example/operations-scope-e2e", Subject: userID}, nil
		}),
		TenantResolver: TenantResolverFunc(func(context.Context, Claims) (Tenant, error) {
			return Tenant{TenantID: tenantID, Type: tenantType}, nil
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

// TestCreateEnvironmentQuotaScopesToTheOperationsCallersOwnTenant drives the
// literal failure scenario over HTTP: an OPERATIONS tenant whose own quota
// cap is 1 must be able to create its first environment regardless of how
// many environments a stranger tenant holds, and must then be refused its
// second exactly the way a correctly-scoped quota check refuses anyone once
// their own cap is reached.
func TestCreateEnvironmentQuotaScopesToTheOperationsCallersOwnTenant(t *testing.T) {
	opsCtx, strangerCtx, opsTenantID, strangerTenantID, db := operationsScopeDatabase(t)
	quotas := repository.NewTenantQuotaRepository(repository.NewTxManager(db, repository.DialectPostgres))
	_, err := quotas.Set(opsCtx, opsTenantID, model.TenantQuota{MaxEnvironments: 1})
	mustNoErr(t, err, "cap the operations tenant's own quota at 1")

	opsSecurity, _ := security.FromContext(opsCtx)
	strangerSecurity, _ := security.FromContext(strangerCtx)
	var opsUserID, strangerUserID string
	mustNoErr(t, db.QueryRow(`INSERT INTO users (tenant_id, username) VALUES ($1, 'ops-caller') RETURNING user_id`, opsSecurity.TenantID).Scan(&opsUserID), "seed ops user")
	mustNoErr(t, db.QueryRow(`INSERT INTO users (tenant_id, username) VALUES ($1, 'stranger-caller') RETURNING user_id`, strangerSecurity.TenantID).Scan(&strangerUserID), "seed stranger user")
	grantAllAccessRole(t, db, opsTenantID, opsUserID)
	grantAllAccessRole(t, db, strangerTenantID, strangerUserID)

	opsServer := startEnvironmentsAPIServer(t, db, opsTenantID, model.TenantTypeOperations, opsUserID)
	strangerServer := startEnvironmentsAPIServer(t, db, strangerTenantID, model.TenantTypeCompany, strangerUserID)

	// The stranger tenant holds more environments than the operations
	// tenant's own cap. Type remote-agent skips cluster placement entirely,
	// so this needs nothing beyond the database.
	for _, name := range []string{"stranger-env-1", "stranger-env-2"} {
		code, body := e2eRequest(t, strangerServer.URL, http.MethodPost, "/v1/environments", map[string]any{"name": name, "type": "remote-agent"})
		if code != http.StatusCreated {
			t.Fatalf("create %s for stranger: HTTP %d: %s", name, code, body)
		}
	}

	code, body := e2eRequest(t, opsServer.URL, http.MethodPost, "/v1/environments", map[string]any{"name": "ops-env-1", "type": "remote-agent"})
	if code != http.StatusCreated {
		t.Fatalf("operations tenant's first create: HTTP %d (want 201 — its own count is 0, not the stranger's 2): %s", code, body)
	}

	code, body = e2eRequest(t, opsServer.URL, http.MethodPost, "/v1/environments", map[string]any{"name": "ops-env-2", "type": "remote-agent"})
	if code != http.StatusConflict {
		t.Fatalf("operations tenant's second create: HTTP %d (want 409 — its own cap of 1 is now reached): %s", code, body)
	}
}

// TestContextListScopesToTheOperationsCallersOwnTenant pins the same failure
// scenario for cloud contexts: an OPERATIONS caller's list must not include a
// stranger tenant's contexts even though erun_operations' RLS policy makes
// them visible too.
func TestContextListScopesToTheOperationsCallersOwnTenant(t *testing.T) {
	opsCtx, strangerCtx, _, _, db := operationsScopeDatabase(t)
	contexts := repository.NewContextRepository(repository.NewTxManager(db, repository.DialectPostgres))

	_, err := contexts.Create(strangerCtx, model.Context{Name: "stranger-context", Provider: "aws"})
	mustNoErr(t, err, "create stranger context")
	own, err := contexts.Create(opsCtx, model.Context{Name: "ops-context", Provider: "aws"})
	mustNoErr(t, err, "create ops context")

	list, err := contexts.List(opsCtx)
	mustNoErr(t, err, "list as operations caller")
	if len(list) != 1 || list[0].ContextID != own.ContextID {
		t.Fatalf("List = %v, want exactly [%s] (the operations caller's own context, not the stranger's as well)", list, own.ContextID)
	}
}

// TestUsageEventListScopesToTheOperationsCallersOwnTenant pins the same
// failure scenario for metering events: an OPERATIONS caller's list must not
// include a stranger tenant's usage events even though erun_operations' RLS
// policy makes them visible too.
func TestUsageEventListScopesToTheOperationsCallersOwnTenant(t *testing.T) {
	opsCtx, strangerCtx, _, _, db := operationsScopeDatabase(t)
	usageEvents := repository.NewUsageEventRepository(repository.NewTxManager(db, repository.DialectPostgres))

	mustNoErr(t, usageEvents.Record(strangerCtx, model.UsageEvent{EventType: "environment_provisioned"}), "record stranger usage event")
	mustNoErr(t, usageEvents.Record(opsCtx, model.UsageEvent{EventType: "environment_provisioned"}), "record ops usage event")

	list, err := usageEvents.List(opsCtx)
	mustNoErr(t, err, "list as operations caller")
	if len(list) != 1 {
		t.Fatalf("List = %v, want exactly 1 event (the operations caller's own, not the stranger's as well)", list)
	}
}

// TestRoleListScopesToTheOperationsCallersOwnTenant pins the worst-shaped
// instance of this defect: RoleRepository.List already reads the security
// context (to bootstrap the predefined TenantUser/TenantAdmin roles), which
// makes it look scoped even though it used to apply that TenantID nowhere in
// either SELECT. An OPERATIONS caller's list must contain only its own
// tenant's roles.
func TestRoleListScopesToTheOperationsCallersOwnTenant(t *testing.T) {
	opsCtx, strangerCtx, opsTenantID, _, db := operationsScopeDatabase(t)
	roles := repository.NewRoleRepository(repository.NewTxManager(db, repository.DialectPostgres))

	_, err := roles.Create(strangerCtx, "StrangerRole", []repository.RolePermissionInput{
		{APIMethod: "GET", APIPath: "/v1/reviews"},
	})
	mustNoErr(t, err, "create stranger role")
	own, err := roles.Create(opsCtx, "OpsRole", []repository.RolePermissionInput{
		{APIMethod: "GET", APIPath: "/v1/reviews"},
	})
	mustNoErr(t, err, "create ops role")

	list, err := roles.List(opsCtx)
	mustNoErr(t, err, "list as operations caller")
	var foundOwn bool
	for _, role := range list {
		if role.TenantID != opsTenantID {
			t.Fatalf("List returned role %q from tenant %s, want only the operations caller's own tenant %s", role.Name, role.TenantID, opsTenantID)
		}
		if role.RoleID == own.RoleID {
			foundOwn = true
			if len(role.Permissions) != 1 || role.Permissions[0].APIPath != "/v1/reviews" {
				t.Fatalf("expected the created role's permission to round-trip, got %+v", role.Permissions)
			}
		}
	}
	if !foundOwn {
		t.Fatalf("List = %v, want the operations caller's own OpsRole included", list)
	}
}

// seedScopeTestUser creates a user in tenantID with the AllAccess role, so an
// HTTP-level test exercises the tenant-scope behavior itself rather than
// authorization.
func seedScopeTestUser(t *testing.T, db *sql.DB, tenantID, username string) string {
	t.Helper()
	var userID string
	mustNoErr(t, db.QueryRow(`INSERT INTO users (tenant_id, username) VALUES ($1, $2) RETURNING user_id`, tenantID, username).Scan(&userID), "seed user "+username)
	grantAllAccessRole(t, db, tenantID, userID)
	return userID
}

// TestListEnvironmentsHTTPScopesToRequestedTenantForOperationsCaller drives
// GET /v1/environments?tenantId=<target> over HTTP: an operations
// caller naming another tenant sees exactly that tenant's environments, and
// a company caller making the identical request is refused before any row
// is read, proving the boundary is enforced server-side rather than by the
// caller's own good behavior.
func TestListEnvironmentsHTTPScopesToRequestedTenantForOperationsCaller(t *testing.T) {
	_, strangerCtx, opsTenantID, strangerTenantID, db := operationsScopeDatabase(t)
	environments := repository.NewEnvironmentRepository(repository.NewTxManager(db, repository.DialectPostgres))
	strangerEnv, err := environments.Create(strangerCtx, model.Environment{Name: "stranger-env", Type: model.EnvironmentTypeRemoteAgent})
	mustNoErr(t, err, "create stranger environment")

	opsUserID := seedScopeTestUser(t, db, opsTenantID, "ops-caller")
	companyTenantID := seedScopeTestTenant(t, db, "company-scope-e2e", model.TenantTypeCompany)
	t.Cleanup(func() {
		for _, table := range []string{"audit_events", "user_roles", "role_permissions", "roles", "users", "tenants"} {
			if _, err := db.Exec(`DELETE FROM `+table+` WHERE tenant_id = $1`, companyTenantID); err != nil {
				t.Logf("clearing %s for tenant %s: %v", table, companyTenantID, err)
			}
		}
	})
	companyUserID := seedScopeTestUser(t, db, companyTenantID, "company-caller")

	opsServer := startEnvironmentsAPIServer(t, db, opsTenantID, model.TenantTypeOperations, opsUserID)
	companyServer := startEnvironmentsAPIServer(t, db, companyTenantID, model.TenantTypeCompany, companyUserID)

	code, body := e2eRequest(t, opsServer.URL, http.MethodGet, "/v1/environments?tenantId="+strangerTenantID, nil)
	if code != http.StatusOK {
		t.Fatalf("operations caller scoped list: HTTP %d: %s", code, body)
	}
	var listed []model.Environment
	mustNoErr(t, json.Unmarshal([]byte(body), &listed), "parse list response")
	if len(listed) != 1 || listed[0].EnvironmentID != strangerEnv.EnvironmentID || listed[0].TenantID != strangerTenantID {
		t.Fatalf("scoped list = %s, want exactly the stranger tenant's one environment (tenantId %s)", body, strangerTenantID)
	}

	code, body = e2eRequest(t, companyServer.URL, http.MethodGet, "/v1/environments?tenantId="+strangerTenantID, nil)
	if code != http.StatusForbidden {
		t.Fatalf("company caller scoped list: HTTP %d (want 403 — only an operations tenant may target another tenant): %s", code, body)
	}
}

// auditParametersColumn reads api_parameters back directly: the audit read
// API never selects it (see model.AuditEvent's own comment on why), so a
// test proving what was recorded has to go straight to the table.
func auditParametersColumn(t *testing.T, db *sql.DB, tenantID, apiPath string) string {
	t.Helper()
	var parameters sql.NullString
	mustNoErr(t, db.QueryRow(
		`SELECT api_parameters FROM audit_events WHERE tenant_id = $1 AND api_path = $2 AND api_parameters IS NOT NULL ORDER BY created_at DESC LIMIT 1`,
		tenantID, apiPath,
	).Scan(&parameters), "read audit api_parameters")
	return parameters.String
}

// TestCreateEnvironmentHTTPCrossTenantCreatesInTargetTenantAndAudits proves
// the write half server-side, end to end: an operations caller's POST
// /v1/environments naming tenantId places the row in the target tenant (read
// back directly, not trusted from the response), and a distinct audit event
// records the operator's home tenant plus the target in api_parameters —
// the attribution a cross-tenant write needs, since the environment row
// itself, once persisted, only ever names the target.
func TestCreateEnvironmentHTTPCrossTenantCreatesInTargetTenantAndAudits(t *testing.T) {
	opsCtx, _, opsTenantID, strangerTenantID, db := operationsScopeDatabase(t)
	opsSecurity, _ := security.FromContext(opsCtx)
	opsUserID := seedScopeTestUser(t, db, opsTenantID, "ops-caller")
	opsServer := startEnvironmentsAPIServer(t, db, opsTenantID, model.TenantTypeOperations, opsUserID)

	code, body := e2eRequest(t, opsServer.URL, http.MethodPost, "/v1/environments", map[string]any{
		"name": "cross-tenant-env", "type": "remote-agent", "tenantId": strangerTenantID,
	})
	if code != http.StatusCreated {
		t.Fatalf("operations caller cross-tenant create: HTTP %d: %s", code, body)
	}
	var created model.Environment
	mustNoErr(t, json.Unmarshal([]byte(body), &created), "parse create response")

	var persistedTenantID string
	mustNoErr(t, db.QueryRow(`SELECT tenant_id FROM environments WHERE environment_id = $1`, created.EnvironmentID).Scan(&persistedTenantID), "read persisted tenant_id")
	if persistedTenantID != strangerTenantID {
		t.Fatalf("persisted tenant_id = %q, want %q (the target tenant, not the operator's own %q)", persistedTenantID, strangerTenantID, opsTenantID)
	}

	parameters := auditParametersColumn(t, db, opsSecurity.TenantID, "/v1/environments")
	if parameters == "" {
		t.Fatal("expected an audit event with api_parameters recording the cross-tenant create, found none")
	}
	var decoded struct {
		TargetTenantID string `json:"targetTenantId"`
		Name           string `json:"name"`
	}
	mustNoErr(t, json.Unmarshal([]byte(parameters), &decoded), "decode api_parameters")
	if decoded.TargetTenantID != strangerTenantID || decoded.Name != "cross-tenant-env" {
		t.Fatalf("api_parameters = %+v, want targetTenantId=%s name=cross-tenant-env", decoded, strangerTenantID)
	}
}

// TestCreateEnvironmentHTTPCrossTenantIsRefusedForNonOperationsCaller is the
// unentitled-write refusal test: a company caller naming another
// tenant on create is refused server-side with 403, and — the property that
// actually matters — no row is created anywhere, not even in the caller's
// own tenant, proving the refusal happens before any write is attempted.
func TestCreateEnvironmentHTTPCrossTenantIsRefusedForNonOperationsCaller(t *testing.T) {
	_, _, _, strangerTenantID, db := operationsScopeDatabase(t)
	strangerUserID := seedScopeTestUser(t, db, strangerTenantID, "stranger-caller")
	strangerServer := startEnvironmentsAPIServer(t, db, strangerTenantID, model.TenantTypeCompany, strangerUserID)

	otherTenantID := seedScopeTestTenant(t, db, "other-scope-e2e", model.TenantTypeCompany)
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM tenants WHERE tenant_id = $1`, otherTenantID); err != nil {
			t.Logf("clearing tenant %s: %v", otherTenantID, err)
		}
	})

	code, body := e2eRequest(t, strangerServer.URL, http.MethodPost, "/v1/environments", map[string]any{
		"name": "should-not-exist", "type": "remote-agent", "tenantId": otherTenantID,
	})
	if code != http.StatusForbidden {
		t.Fatalf("company caller cross-tenant create: HTTP %d (want 403): %s", code, body)
	}

	var count int
	mustNoErr(t, db.QueryRow(`SELECT COUNT(*) FROM environments WHERE name = 'should-not-exist'`).Scan(&count), "count environments")
	if count != 0 {
		t.Fatalf("expected no environment to be created by a refused cross-tenant request, found %d", count)
	}
}
