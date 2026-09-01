package backendapi

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// The upsert-by-natural-key contract (a later report replaces the row, an
// omitted tool carries the previous one forward) and the tenant boundary on
// ai_sessions.environment_id's composite FK both live in SQL, so both are
// exercised against a real migrated PostgreSQL rather than a fake repository
// that only ever agrees with itself. Mirrors environmentDeleteDatabase's
// shape (environment_delete_e2e_test.go).
func aiSessionsDatabase(t *testing.T) (*repository.AISessionRepository, *repository.EnvironmentRepository, context.Context, string) {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_AI_SESSIONS_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_AI_SESSIONS_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	tenantID := seedAISessionTestTenant(t, db)
	ctx := security.WithContext(context.Background(), security.Context{TenantID: tenantID, TenantType: "COMPANY"})
	txManager := repository.NewTxManager(db, repository.DialectPostgres)
	sessions := repository.NewAISessionRepository(txManager)
	environments := repository.NewEnvironmentRepository(txManager)
	t.Cleanup(func() {
		// environments has no ON DELETE CASCADE from tenants (unlike
		// ai_sessions' own cascade from environments), so it must go first.
		if _, err := db.Exec(`DELETE FROM environments WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing the test tenant's environments (cascades ai_sessions): %v", err)
		}
		if _, err := db.Exec(`DELETE FROM tenants WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing the test tenant: %v", err)
		}
	})
	return sessions, environments, ctx, tenantID
}

func seedAISessionTestTenant(t *testing.T, db *sql.DB) string {
	t.Helper()
	var tenantID string
	err := db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`,
		"ai-sessions-e2e-"+time.Now().Format("20060102150405.000000"),
	).Scan(&tenantID)
	mustNoErr(t, err, "seed tenant")
	return tenantID
}

// TestAISessionRepositoryRecordCarriesForwardTheToolOnAnOmittedReport pins
// the property eruncommon.RecordAISessionEvent enforces locally
// (previouslyReportedAISessionTool): a later event that omits Tool must not
// blank out the one a prior event already reported, so a long-running
// session's tool column never goes empty mid-conversation just because a
// bare turn-end omitted it.
func TestAISessionRepositoryRecordCarriesForwardTheToolOnAnOmittedReport(t *testing.T) {
	sessions, environments, ctx, _ := aiSessionsDatabase(t)
	env, err := environments.Create(ctx, model.Environment{Name: "prod", Type: model.EnvironmentTypeRuntime})
	mustNoErr(t, err, "create environment")

	first, err := sessions.Record(ctx, model.AISessionEvent{EnvironmentID: env.EnvironmentID, SessionID: "ai", Tool: "claude", Event: "turn-start"})
	mustNoErr(t, err, "record first event")
	if first.Tool != "claude" {
		t.Fatalf("tool after first report = %q, want %q", first.Tool, "claude")
	}

	second, err := sessions.Record(ctx, model.AISessionEvent{EnvironmentID: env.EnvironmentID, SessionID: "ai", Event: "turn-end"})
	mustNoErr(t, err, "record second event")
	if second.Tool != "claude" {
		t.Fatalf("tool after an omitted-tool report = %q, want carried-forward %q", second.Tool, "claude")
	}
	if second.Event != "turn-end" {
		t.Fatalf("event after second report = %q, want %q", second.Event, "turn-end")
	}
}

// TestAISessionRepositoryRecordReplacesRatherThanAccumulates pins the
// "one row per (environment, session), latest event wins" contract: a second
// report for the same session must update the existing row, not create a
// second one a List call would then double-count.
func TestAISessionRepositoryRecordReplacesRatherThanAccumulates(t *testing.T) {
	sessions, environments, ctx, _ := aiSessionsDatabase(t)
	env, err := environments.Create(ctx, model.Environment{Name: "prod", Type: model.EnvironmentTypeRuntime})
	mustNoErr(t, err, "create environment")

	for _, event := range []string{"turn-start", "tool-use", "turn-end"} {
		_, err := sessions.Record(ctx, model.AISessionEvent{EnvironmentID: env.EnvironmentID, SessionID: "ai", Tool: "claude", Event: event})
		mustNoErr(t, err, "record "+event)
	}

	all, err := sessions.List(ctx, env.EnvironmentID)
	mustNoErr(t, err, "list")
	if len(all) != 1 {
		t.Fatalf("sessions for environment = %d, want 1 (replace, not accumulate)", len(all))
	}
	if all[0].Event != "turn-end" {
		t.Fatalf("event = %q, want the latest report %q", all[0].Event, "turn-end")
	}
}

// TestAISessionRepositoryListIsScopedToOneEnvironment proves List does not
// leak a session reported against a sibling environment in the same tenant.
func TestAISessionRepositoryListIsScopedToOneEnvironment(t *testing.T) {
	sessions, environments, ctx, _ := aiSessionsDatabase(t)
	envA, err := environments.Create(ctx, model.Environment{Name: "prod-a", Type: model.EnvironmentTypeRuntime})
	mustNoErr(t, err, "create environment a")
	envB, err := environments.Create(ctx, model.Environment{Name: "prod-b", Type: model.EnvironmentTypeRuntime})
	mustNoErr(t, err, "create environment b")

	_, err = sessions.Record(ctx, model.AISessionEvent{EnvironmentID: envA.EnvironmentID, SessionID: "ai", Event: "turn-start"})
	mustNoErr(t, err, "record for env a")
	_, err = sessions.Record(ctx, model.AISessionEvent{EnvironmentID: envB.EnvironmentID, SessionID: "ai", Event: "turn-start"})
	mustNoErr(t, err, "record for env b")

	found, err := sessions.List(ctx, envA.EnvironmentID)
	mustNoErr(t, err, "list env a")
	if len(found) != 1 || found[0].EnvironmentID != envA.EnvironmentID {
		t.Fatalf("List(envA) = %+v, want exactly envA's own session", found)
	}
}

// TestAISessionRepositoryRecordRefusesAnEnvironmentFromAnotherTenant is the
// authorization proof this table needs: the composite foreign key
// (tenant_id, environment_id) -> environments(tenant_id, environment_id)
// means a caller cannot report against another tenant's environment id even
// though environment ids are globally unique UUIDs, because tenant_id always
// defaults to the CALLER's own resolved tenant (erun_current_tenant_id()),
// never the caller-supplied value. There is no environment row for (this
// caller's tenant, that other tenant's environment id), so the insert fails
// closed rather than silently attaching the report to the wrong tenant or to
// nothing.
func TestAISessionRepositoryRecordRefusesAnEnvironmentFromAnotherTenant(t *testing.T) {
	sessionsA, environmentsA, ctxA, _ := aiSessionsDatabase(t)
	envA, err := environmentsA.Create(ctxA, model.Environment{Name: "prod", Type: model.EnvironmentTypeRuntime})
	mustNoErr(t, err, "create environment for tenant a")

	sessionsB, _, ctxB, tenantB := aiSessionsDatabase(t)
	_ = sessionsA
	_, err = sessionsB.Record(ctxB, model.AISessionEvent{EnvironmentID: envA.EnvironmentID, SessionID: "ai", Event: "turn-start"})
	if err == nil {
		t.Fatalf("tenant %s recording against tenant a's environment id should be refused, got no error", tenantB)
	}
}
