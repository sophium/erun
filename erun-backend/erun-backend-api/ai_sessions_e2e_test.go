package backendapi

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/testpg"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// The upsert and tenant-boundary contracts live in SQL, so these run against a
// real migrated PostgreSQL.
func aiSessionsDatabase(t *testing.T) (*repository.AISessionRepository, *repository.EnvironmentRepository, context.Context, string) {
	t.Helper()
	databaseURL := testpg.URL(t)
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	tenantID := seedAISessionTestTenant(t, db)
	ctx := security.WithContext(context.Background(), security.Context{TenantID: tenantID, TenantType: "COMPANY"})
	txManager := repository.NewTxManager(db, repository.DialectPostgres)
	sessions := repository.NewAISessionRepository(txManager)
	environments := repository.NewEnvironmentRepository(txManager)
	t.Cleanup(func() {
		// environments has no ON DELETE CASCADE from tenants, so it must go first.
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

// aiSessionsOperationsDatabase seeds an OPERATIONS tenant, whose RLS policy is
// unconditional, so List's own tenant guard is what is exercised.
func aiSessionsOperationsDatabase(t *testing.T, db *sql.DB) (*repository.AISessionRepository, *repository.EnvironmentRepository, context.Context) {
	t.Helper()
	var tenantID string
	err := db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'OPERATIONS') RETURNING tenant_id`,
		"ai-sessions-e2e-ops-"+time.Now().Format("20060102150405.000000"),
	).Scan(&tenantID)
	mustNoErr(t, err, "seed operations tenant")
	ctx := security.WithContext(context.Background(), security.Context{TenantID: tenantID, TenantType: "OPERATIONS"})
	txManager := repository.NewTxManager(db, repository.DialectPostgres)
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM environments WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing the operations tenant's environments (cascades ai_sessions): %v", err)
		}
		if _, err := db.Exec(`DELETE FROM tenants WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing the operations tenant: %v", err)
		}
	})
	return repository.NewAISessionRepository(txManager), repository.NewEnvironmentRepository(txManager), ctx
}

func TestAISessionRepositoryListScopesToTheOperationsCallersOwnTenant(t *testing.T) {
	strangerSessions, strangerEnvironments, strangerCtx, _ := aiSessionsDatabase(t)
	strangerEnv, err := strangerEnvironments.Create(strangerCtx, model.Environment{Name: "stranger-env", Type: model.EnvironmentTypeRuntime})
	mustNoErr(t, err, "create stranger environment")
	_, err = strangerSessions.Record(strangerCtx, model.AISessionEvent{EnvironmentID: strangerEnv.EnvironmentID, SessionID: "ai", Event: "turn-start"})
	mustNoErr(t, err, "record stranger session event")

	databaseURL := testpg.URL(t)
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })
	opsSessions, opsEnvironments, opsCtx := aiSessionsOperationsDatabase(t, db)
	opsEnv, err := opsEnvironments.Create(opsCtx, model.Environment{Name: "ops-env", Type: model.EnvironmentTypeRuntime})
	mustNoErr(t, err, "create ops environment")
	_, err = opsSessions.Record(opsCtx, model.AISessionEvent{EnvironmentID: opsEnv.EnvironmentID, SessionID: "ai", Event: "turn-start"})
	mustNoErr(t, err, "record ops session event")

	// erun_operations bypasses RLS; only the explicit tenant_id predicate guards this.
	crossTenant, err := opsSessions.List(opsCtx, strangerEnv.EnvironmentID)
	mustNoErr(t, err, "list stranger's environment id as operations caller")
	if len(crossTenant) != 0 {
		t.Fatalf("List(strangerEnv) as operations caller = %+v, want empty", crossTenant)
	}

	own, err := opsSessions.List(opsCtx, opsEnv.EnvironmentID)
	mustNoErr(t, err, "list own environment id as operations caller")
	if len(own) != 1 || own[0].EnvironmentID != opsEnv.EnvironmentID {
		t.Fatalf("List(opsEnv) as operations caller = %+v, want exactly its own session", own)
	}
}

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

// The composite FK plus tenant_id defaulting to the caller's tenant makes the
// insert fail closed.
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
