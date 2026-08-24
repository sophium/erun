package backendapi

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// Row-level security is the entire authorization boundary for reading audit
// events, so it is exercised against a real migrated PostgreSQL rather than a
// fake that would just agree with itself about which rows a tenant may see.
func auditEventDatabase(t *testing.T) (*repository.AuditEventRepository, *sql.DB) {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_AUDIT_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_AUDIT_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })
	return repository.NewAuditEventRepository(repository.NewTxManager(db, repository.DialectPostgres)), db
}

// seedAuditTenant creates a tenant and one user of its own for the scenario,
// so a run never disturbs rows another tenant owns and RLS is actually
// exercised. audit_events.erun_user_id foreign-keys (tenant_id, user_id), so a
// user row is required before an audit event can be logged for it.
func seedAuditTenant(t *testing.T, db *sql.DB) (tenantID string, userID string) {
	t.Helper()
	stamp := time.Now().Format("20060102150405.000000")
	err := db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`,
		"audit-e2e-"+stamp,
	).Scan(&tenantID)
	mustNoErr(t, err, "seed tenant")
	err = db.QueryRow(
		`INSERT INTO users (tenant_id, username) VALUES ($1, $2) RETURNING user_id`,
		tenantID, "audit-e2e-user-"+stamp,
	).Scan(&userID)
	mustNoErr(t, err, "seed user")
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM audit_events WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing the test tenant's audit events: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM users WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing the test tenant's users: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM tenants WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing the test tenant: %v", err)
		}
	})
	return tenantID, userID
}

// seedAuditUser adds a second user to an already-seeded tenant, for scenarios
// that need two distinct erun_user_id values within one tenant.
func seedAuditUser(t *testing.T, db *sql.DB, tenantID string) string {
	t.Helper()
	var userID string
	err := db.QueryRow(
		`INSERT INTO users (tenant_id, username) VALUES ($1, $2) RETURNING user_id`,
		tenantID, "audit-e2e-user-"+time.Now().Format("20060102150405.000000000"),
	).Scan(&userID)
	mustNoErr(t, err, "seed second user")
	return userID
}

func auditContext(tenantID, userID string) context.Context {
	return security.WithContext(context.Background(), security.Context{
		TenantID: tenantID, TenantType: "COMPANY", ErunUserID: userID,
	})
}

func logAuditEvent(t *testing.T, repo *repository.AuditEventRepository, ctx context.Context, event model.AuditEvent) {
	t.Helper()
	mustNoErr(t, repo.LogAuditEvent(ctx, event), "log audit event")
}

// TestAuditEventListIsScopedByTenant is the invariant the whole read API rests
// on: a caller from one tenant must never see another tenant's audit rows,
// even though both are simple SELECTs behind the same endpoint.
func TestAuditEventListIsScopedByTenant(t *testing.T) {
	repo, db := auditEventDatabase(t)
	tenantA, userA := seedAuditTenant(t, db)
	ctxA := auditContext(tenantA, userA)
	logAuditEvent(t, repo, ctxA, model.AuditEvent{
		TenantID: tenantA, ErunUserID: userA, ExternalUserID: "ext-a", ExternalIssuerID: "https://issuer.example/a",
		Type: model.AuditEventTypeAPI, APIMethod: "GET", APIPath: "/v1/reviews",
	})

	tenantB, userB := seedAuditTenant(t, db)
	ctxB := auditContext(tenantB, userB)
	logAuditEvent(t, repo, ctxB, model.AuditEvent{
		TenantID: tenantB, ErunUserID: userB, ExternalUserID: "ext-b", ExternalIssuerID: "https://issuer.example/b",
		Type: model.AuditEventTypeAPI, APIMethod: "GET", APIPath: "/v1/reviews",
	})

	pageA, err := repo.List(ctxA, repository.AuditEventFilter{})
	mustNoErr(t, err, "list tenant A")
	if len(pageA.Events) != 1 || pageA.Events[0].TenantID != tenantA {
		t.Fatalf("tenant A saw %+v, want exactly its own one event", pageA.Events)
	}

	pageB, err := repo.List(ctxB, repository.AuditEventFilter{})
	mustNoErr(t, err, "list tenant B")
	if len(pageB.Events) != 1 || pageB.Events[0].TenantID != tenantB {
		t.Fatalf("tenant B saw %+v, want exactly its own one event", pageB.Events)
	}
	if pageA.Events[0].AuditEventID == pageB.Events[0].AuditEventID {
		t.Fatal("both tenants resolved to the same audit event id")
	}
}

// TestAuditEventListNeverExposesParameterPayloads proves the read path holds
// even when a row does carry recorded arguments: an MCP tool such as
// cloud_inject_aws_credentials takes credentials as arguments, and this is
// where a future MCP audit caller would have serialized them.
func TestAuditEventListNeverExposesParameterPayloads(t *testing.T) {
	repo, db := auditEventDatabase(t)
	tenantID, userID := seedAuditTenant(t, db)
	ctx := auditContext(tenantID, userID)
	logAuditEvent(t, repo, ctx, model.AuditEvent{
		TenantID: tenantID, ErunUserID: userID, ExternalUserID: "ext", ExternalIssuerID: "https://issuer.example",
		Type: model.AuditEventTypeMCP, MCPTool: "cloud_inject_aws_credentials",
		MCPToolParameters: `{"accessKeyId":"AKIAEXAMPLE","secretAccessKey":"super-secret"}`,
	})

	page, err := repo.List(ctx, repository.AuditEventFilter{})
	mustNoErr(t, err, "list")
	if len(page.Events) != 1 {
		t.Fatalf("got %d events, want 1", len(page.Events))
	}
	event := page.Events[0]
	if event.MCPTool != "cloud_inject_aws_credentials" {
		t.Fatalf("mcpTool = %q, want the tool name preserved", event.MCPTool)
	}
	if event.MCPToolParameters != "" {
		t.Fatalf("MCPToolParameters leaked through List: %q", event.MCPToolParameters)
	}
}

// TestAuditEventListFiltersMatchTheirIndexedColumns exercises the filters the
// three audit_events indexes exist for: erun_user_id and (api_method, api_path).
func TestAuditEventListFiltersMatchTheirIndexedColumns(t *testing.T) {
	repo, db := auditEventDatabase(t)
	tenantID, userA := seedAuditTenant(t, db)
	userB := seedAuditUser(t, db, tenantID)
	ctxA := auditContext(tenantID, userA)
	ctxB := auditContext(tenantID, userB)

	logAuditEvent(t, repo, ctxA, model.AuditEvent{
		TenantID: tenantID, ErunUserID: userA, ExternalUserID: "a", ExternalIssuerID: "https://issuer.example",
		Type: model.AuditEventTypeAPI, APIMethod: "GET", APIPath: "/v1/reviews",
	})
	logAuditEvent(t, repo, ctxB, model.AuditEvent{
		TenantID: tenantID, ErunUserID: userB, ExternalUserID: "b", ExternalIssuerID: "https://issuer.example",
		Type: model.AuditEventTypeAPI, APIMethod: "POST", APIPath: "/v1/reviews",
	})
	logAuditEvent(t, repo, ctxB, model.AuditEvent{
		TenantID: tenantID, ErunUserID: userB, ExternalUserID: "b", ExternalIssuerID: "https://issuer.example",
		Type: model.AuditEventTypeCLI, CLICommand: "erun build",
	})

	byUser, err := repo.List(ctxA, repository.AuditEventFilter{ErunUserID: userA})
	mustNoErr(t, err, "list by erun user id")
	if len(byUser.Events) != 1 || byUser.Events[0].ErunUserID != userA {
		t.Fatalf("filter by erunUserId = %+v, want exactly userA's event", byUser.Events)
	}

	byMethodPath, err := repo.List(ctxA, repository.AuditEventFilter{APIMethod: "POST", APIPath: "/v1/reviews"})
	mustNoErr(t, err, "list by api method/path")
	if len(byMethodPath.Events) != 1 || byMethodPath.Events[0].ErunUserID != userB {
		t.Fatalf("filter by apiMethod/apiPath = %+v, want exactly the POST event", byMethodPath.Events)
	}

	byType, err := repo.List(ctxA, repository.AuditEventFilter{Type: model.AuditEventTypeCLI})
	mustNoErr(t, err, "list by type")
	if len(byType.Events) != 1 || byType.Events[0].CLICommand != "erun build" {
		t.Fatalf("filter by type=CLI = %+v, want exactly the CLI event", byType.Events)
	}

	all, err := repo.List(ctxA, repository.AuditEventFilter{})
	mustNoErr(t, err, "list unfiltered")
	if len(all.Events) != 3 {
		t.Fatalf("unfiltered list returned %d events, want all 3 for the tenant", len(all.Events))
	}
}

// TestAuditEventListPaginatesStablyAcrossInserts is the DOS-shaped concern the
// issue calls out: audit_events is append-only and unbounded, so paging must
// use the (created_at, audit_event_id) keyset rather than an offset that a
// concurrent insert would shift under the caller.
func TestAuditEventListPaginatesStablyAcrossInserts(t *testing.T) {
	repo, db := auditEventDatabase(t)
	tenantID, userID := seedAuditTenant(t, db)
	ctx := auditContext(tenantID, userID)

	const total = 5
	for i := 0; i < total; i++ {
		logAuditEvent(t, repo, ctx, model.AuditEvent{
			TenantID: tenantID, ErunUserID: userID, ExternalUserID: "u", ExternalIssuerID: "https://issuer.example",
			Type: model.AuditEventTypeCLI, CLICommand: "erun build",
		})
	}
	seeded := auditEventIDs(t, repo, ctx)
	if len(seeded) != total {
		t.Fatalf("seeded listing returned %d events, want %d", len(seeded), total)
	}

	seen := pageThroughInsertingBetweenPages(t, repo, ctx, tenantID, userID, total)
	if len(seen) != total {
		t.Fatalf("paged through %d distinct events, want exactly the original %d", len(seen), total)
	}
	for _, id := range seeded {
		if !seen[id] {
			t.Fatalf("original event %s was skipped by pagination", id)
		}
	}
}

func auditEventIDs(t *testing.T, repo *repository.AuditEventRepository, ctx context.Context) []string {
	t.Helper()
	page, err := repo.List(ctx, repository.AuditEventFilter{})
	mustNoErr(t, err, "list")
	ids := make([]string, 0, len(page.Events))
	for _, e := range page.Events {
		ids = append(ids, e.AuditEventID)
	}
	return ids
}

// pageThroughInsertingBetweenPages pages one row at a time, inserting a new
// row after each page is fetched — the shape of a table receiving writes while
// a caller is still paging through it — and returns the audit event ids seen.
func pageThroughInsertingBetweenPages(
	t *testing.T, repo *repository.AuditEventRepository, ctx context.Context, tenantID, userID string, total int,
) map[string]bool {
	t.Helper()
	seen := make(map[string]bool)
	cursor := repository.AuditEventCursor{}
	for i := 0; i < total; i++ { // bounded: one row per page can never need more pages than rows.
		page, err := repo.List(ctx, repository.AuditEventFilter{Limit: 1, Cursor: cursor})
		mustNoErr(t, err, "page")
		if len(page.Events) != 1 {
			t.Fatalf("page %d returned %d events, want 1", i, len(page.Events))
		}

		// A new row lands mid-pagination. It sorts newest-first, ahead of
		// every page already handed out, so it must not appear in — or shift
		// — any page still to come.
		logAuditEvent(t, repo, ctx, model.AuditEvent{
			TenantID: tenantID, ErunUserID: userID, ExternalUserID: "u", ExternalIssuerID: "https://issuer.example",
			Type: model.AuditEventTypeCLI, CLICommand: "erun build (inserted mid-page)",
		})

		id := page.Events[0].AuditEventID
		if seen[id] {
			t.Fatalf("page %d repeated audit event %s", i, id)
		}
		seen[id] = true

		if page.NextCursor == "" {
			if i != total-1 {
				t.Fatalf("page %d reported no next page after only %d of %d rows", i, i+1, total)
			}
			break
		}
		cursor, err = repository.ParseAuditEventCursor(page.NextCursor)
		mustNoErr(t, err, "parse next cursor")
	}
	return seen
}

// TestAuditEventListReportsNextCursorOnlyWhenMoreRemain guards the boundary of
// the limit+1 lookahead: the last real page must not claim a next page exists.
func TestAuditEventListReportsNextCursorOnlyWhenMoreRemain(t *testing.T) {
	repo, db := auditEventDatabase(t)
	tenantID, userID := seedAuditTenant(t, db)
	ctx := auditContext(tenantID, userID)
	logAuditEvent(t, repo, ctx, model.AuditEvent{
		TenantID: tenantID, ErunUserID: userID, ExternalUserID: "u", ExternalIssuerID: "https://issuer.example",
		Type: model.AuditEventTypeCLI, CLICommand: "erun build",
	})

	page, err := repo.List(ctx, repository.AuditEventFilter{Limit: 5})
	mustNoErr(t, err, "list")
	if len(page.Events) != 1 {
		t.Fatalf("got %d events, want 1", len(page.Events))
	}
	if page.NextCursor != "" {
		t.Fatalf("nextCursor = %q, want empty: there is exactly one row and the page held it", page.NextCursor)
	}
}

// TestAuditEventListFiltersUseTheirDedicatedIndex proves the filters List
// exposes are the ones the three audit_events indexes were actually built for,
// not filters that would force a sequential scan as the trail grows. It seeds
// enough rows with real selectivity for PostgreSQL's planner to prefer the
// dedicated composite index over the plain tenant/created_at one, rather than
// forcing the choice with enable_seqscan.
func TestAuditEventListFiltersUseTheirDedicatedIndex(t *testing.T) {
	_, db := auditEventDatabase(t)
	tenantID, _ := seedAuditTenant(t, db)
	userIDs := make([]string, 6)
	for i := range userIDs {
		userIDs[i] = seedAuditUser(t, db, tenantID)
	}
	seedBulkAuditEvents(t, db, tenantID, userIDs, 4000)

	targetUser := userIDs[0]
	explainByErunUser := explainAuditEventList(t, db, tenantID,
		`erun_user_id = $1`, []any{targetUser})
	if !strings.Contains(explainByErunUser, "audit_events_tenant_user_created_at_idx") {
		t.Fatalf("erunUserId filter did not use its dedicated index:\n%s", explainByErunUser)
	}

	explainByAPIMethodPath := explainAuditEventList(t, db, tenantID,
		`api_method = $1 AND api_path = $2`, []any{"POST", "/v1/reviews"})
	if !strings.Contains(explainByAPIMethodPath, "audit_events_tenant_api_created_at_idx") {
		t.Fatalf("apiMethod/apiPath filter did not use its dedicated index:\n%s", explainByAPIMethodPath)
	}

	explainUnfiltered := explainAuditEventList(t, db, tenantID, `TRUE`, nil)
	if strings.Contains(explainUnfiltered, "Seq Scan") {
		t.Fatalf("unfiltered list fell back to a sequential scan:\n%s", explainUnfiltered)
	}
}

// seedBulkAuditEvents inserts n rows directly (bypassing LogAuditEvent, which
// would take one round trip per row) spread across userIDs and two API
// methods, so erun_user_id and api_method actually narrow the row set instead
// of every value being equally represented.
func seedBulkAuditEvents(t *testing.T, db *sql.DB, tenantID string, userIDs []string, n int) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO audit_events (
			tenant_id, erun_user_id, external_user_id, external_issuer_id, type, api_method, api_path, created_at
		)
		SELECT
			$1,
			($2::uuid[])[1 + (s.n % array_length($2::uuid[], 1))],
			'ext', 'https://issuer.example', 'API',
			(ARRAY['GET', 'POST'])[1 + (s.n % 2)],
			'/v1/reviews',
			NOW() - (s.n || ' seconds')::interval
		FROM generate_series(1, $3) AS s(n)
	`, tenantID, postgresUUIDArrayLiteral(userIDs), n)
	mustNoErr(t, err, "seed bulk audit events")
	_, err = db.Exec(`ANALYZE audit_events`)
	mustNoErr(t, err, "analyze audit_events")
}

// postgresUUIDArrayLiteral formats a Go string slice as a PostgreSQL array literal. The
// values are test-controlled UUIDs from seedAuditUser, never external input.
func postgresUUIDArrayLiteral(values []string) string {
	return "{" + strings.Join(values, ",") + "}"
}

// explainAuditEventList runs EXPLAIN for the same shape of query List builds
// (tenant-scoped by RLS, ordered newest first) inside a transaction with the
// tenant's real security context, and returns the plan as text.
func explainAuditEventList(t *testing.T, db *sql.DB, tenantID string, where string, extraArgs []any) string {
	t.Helper()
	tx, err := db.Begin()
	mustNoErr(t, err, "begin explain tx")
	t.Cleanup(func() { _ = tx.Rollback() })
	_, err = tx.Exec(`SET LOCAL ROLE erun_tenant`)
	mustNoErr(t, err, "set role")
	_, err = tx.Exec(`SELECT set_config('erun.tenant_id', $1, true)`, tenantID)
	mustNoErr(t, err, "set tenant context")
	query := `EXPLAIN SELECT audit_event_id FROM audit_events WHERE ` + where +
		` ORDER BY created_at DESC, audit_event_id DESC LIMIT 51`
	rows, err := tx.Query(query, extraArgs...)
	mustNoErr(t, err, "explain")
	defer func() { _ = rows.Close() }()
	var plan strings.Builder
	for rows.Next() {
		var line string
		mustNoErr(t, rows.Scan(&line), "scan explain line")
		plan.WriteString(line)
		plan.WriteString("\n")
	}
	mustNoErr(t, rows.Err(), "read explain plan")
	return plan.String()
}
