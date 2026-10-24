package backendapi

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/testpg"
)

// Runs erun-backend-db/retention/ai_sessions.sql through retention.sh in a real psql
// container; policy is in erun-backend-db/AGENTS.md ("AI Sessions").

const retentionAISessionsPolicy = "ai_sessions.sql"

func retentionAISessionsCount(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	mustNoErr(t, db.QueryRow(query, args...).Scan(&n), "count query "+query)
	return n
}

// retentionAISessionsEligibleSQL verifies no foreign row is eligible, since the sweep
// is platform-wide and the counts below must be exactly the fixtures'.
const retentionAISessionsEligibleSQL = `
WITH exited_ranked AS (
  SELECT tenant_id, occurred_at,
         row_number() OVER (PARTITION BY tenant_id, environment_id ORDER BY occurred_at DESC) AS rn
  FROM ai_sessions
  WHERE event = 'exit'
)
SELECT count(*) FROM exited_ranked
WHERE (rn > 500 OR occurred_at < now() - interval '14 days')
  AND tenant_id <> ALL ($1::uuid[])`

type retentionAISessionsFixture struct {
	ageTenant, countTenant, env1, envNoisy, envQuiet string
}

func (f retentionAISessionsFixture) tenants() string {
	return "{" + f.ageTenant + "," + f.countTenant + "}"
}

func TestRetentionAISessionsDeletesExactlyTheAgeAndPerEnvironmentCountBoundExitedSessions(t *testing.T) {
	databaseURL := testpg.URL(t)
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	psql := startRetentionPsql(t, ctx, databaseURL, retentionAISessionsPolicy)

	f := retentionAISessionsSeed(t, db)
	initialSessions := retentionAISessionsCheckPreconditions(t, db, f)

	retentionAISessionsAssertDryRun(t, db, psql, f, initialSessions)

	psql.sweep(t, ctx, false)

	eligible, deleted := retentionAISessionsLastRun(t, db, false)
	if eligible != 6 {
		t.Fatalf("the real run must record 6 ai_sessions eligible in retention_runs, got %d", eligible)
	}
	if deleted != 6 {
		t.Fatalf("the real run must record 6 ai_sessions deleted in retention_runs, got %d", deleted)
	}

	retentionAISessionsAssertAgeBound(t, db, f)
	retentionAISessionsAssertCountCap(t, db, f)
}

// env-quiet shares a tenant with env-noisy to prove the cap is per (tenant, environment).
func retentionAISessionsSeed(t *testing.T, db *sql.DB) retentionAISessionsFixture {
	t.Helper()
	suffix := time.Now().Format("20060102150405.000000")
	var f retentionAISessionsFixture
	mustNoErr(t, db.QueryRow(`INSERT INTO tenants (name) VALUES ($1) RETURNING tenant_id`, "age-tenant-"+suffix).Scan(&f.ageTenant), "seed age-tenant")
	mustNoErr(t, db.QueryRow(`INSERT INTO tenants (name) VALUES ($1) RETURNING tenant_id`, "count-tenant-"+suffix).Scan(&f.countTenant), "seed count-tenant")
	t.Cleanup(func() { retentionAISessionsCleanup(t, db, f.ageTenant, f.countTenant) })
	seedEnv := func(tenantID, name string) string {
		var id string
		mustNoErr(t, db.QueryRow(`INSERT INTO environments (tenant_id, name, type) VALUES ($1, $2, 'local-agent') RETURNING environment_id`, tenantID, name).Scan(&id), "seed environment "+name)
		return id
	}
	f.env1 = seedEnv(f.ageTenant, "env1")
	f.envNoisy = seedEnv(f.countTenant, "env-noisy")
	f.envQuiet = seedEnv(f.countTenant, "env-quiet")

	_, err := db.Exec(`
INSERT INTO ai_sessions (tenant_id, environment_id, session_id, event, occurred_at) VALUES
  ($1, $2, 'exit-old', 'exit', now() - interval '30 days'),
  ($1, $2, 'exit-young', 'exit', now() - interval '1 day'),
  ($1, $2, 'busy-old', 'turn-start', now() - interval '30 days')`, f.ageTenant, f.env1)
	mustNoErr(t, err, "seed age-tenant sessions")
	_, err = db.Exec(`
INSERT INTO ai_sessions (tenant_id, environment_id, session_id, event, occurred_at)
SELECT $1, $2, 'noisy-' || i, 'exit', now() - (i * interval '1 second')
FROM generate_series(1, 505) AS i`, f.countTenant, f.envNoisy)
	mustNoErr(t, err, "seed env-noisy sessions")
	_, err = db.Exec(`
INSERT INTO ai_sessions (tenant_id, environment_id, session_id, event, occurred_at)
SELECT $1, $2, 'quiet-' || i, 'exit', now() - (i * interval '1 second')
FROM generate_series(1, 10) AS i`, f.countTenant, f.envQuiet)
	mustNoErr(t, err, "seed env-quiet sessions")
	return f
}

func retentionAISessionsCleanup(t *testing.T, db *sql.DB, tenantIDs ...string) {
	// environments does not cascade from tenants, so it goes first.
	for _, tenantID := range tenantIDs {
		if _, err := db.Exec(`DELETE FROM environments WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing retention fixture environments: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM tenants WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing retention fixture tenant: %v", err)
		}
	}
}

func retentionAISessionsFixtureCount(t *testing.T, db *sql.DB, f retentionAISessionsFixture) int64 {
	t.Helper()
	return retentionAISessionsCount(t, db, `SELECT count(*) FROM ai_sessions WHERE tenant_id = ANY ($1::uuid[])`, f.tenants())
}

func retentionAISessionsCheckPreconditions(t *testing.T, db *sql.DB, f retentionAISessionsFixture) int64 {
	t.Helper()
	if n := retentionAISessionsFixtureCount(t, db, f); n != 518 {
		t.Fatalf("expected 518 fixture ai_sessions (3 + 505 + 10), got %d", n)
	}
	if n := retentionAISessionsCount(t, db, retentionAISessionsEligibleSQL, f.tenants()); n != 0 {
		t.Fatalf("precondition: %d ai_sessions rows outside this test's fixtures are already retention-eligible; the sweep-wide counts would not be the fixtures' alone", n)
	}
	return retentionAISessionsCount(t, db, `SELECT count(*) FROM ai_sessions`)
}

func retentionAISessionsLastRun(t *testing.T, db *sql.DB, dryRun bool) (eligible, deleted int64) {
	t.Helper()
	mustNoErr(t, db.QueryRow(`SELECT eligible_count, deleted_count FROM retention_runs
WHERE policy_name = 'ai_sessions' AND table_name = 'ai_sessions' AND dry_run = $1
ORDER BY created_at DESC LIMIT 1`, dryRun).Scan(&eligible, &deleted), fmt.Sprintf("read retention_runs (dry_run=%t)", dryRun))
	return eligible, deleted
}

func retentionAISessionsAssertDryRun(t *testing.T, db *sql.DB, psql retentionPsql, f retentionAISessionsFixture, initialSessions int64) {
	t.Helper()
	out := psql.sweep(t, context.Background(), true)
	if retentionReport(out)["ai_sessions"] != "6" {
		t.Fatalf("dry run must report 6 ai_sessions eligible (1 age-bound + 5 count-cap on env-noisy), got:\n%s", out)
	}
	if n := retentionAISessionsCount(t, db, `SELECT count(*) FROM ai_sessions`); n != initialSessions {
		t.Fatalf("dry run must not delete any ai_sessions: had %d, now %d", initialSessions, n)
	}
	if n := retentionAISessionsFixtureCount(t, db, f); n != 518 {
		t.Fatalf("dry run must not delete any fixture ai_sessions: now %d of 518", n)
	}

	eligible, deleted := retentionAISessionsLastRun(t, db, true)
	if eligible != 6 {
		t.Fatalf("the dry run must record 6 ai_sessions eligible in retention_runs, got %d", eligible)
	}
	if deleted != 0 {
		t.Fatalf("the dry run must record 0 ai_sessions deleted in retention_runs, got %d", deleted)
	}
}

func retentionAISessionsSessionCount(t *testing.T, db *sql.DB, environmentID, sessionID string) int64 {
	t.Helper()
	return retentionAISessionsCount(t, db, `SELECT count(*) FROM ai_sessions WHERE environment_id = $1 AND session_id = $2`, environmentID, sessionID)
}

func retentionAISessionsAssertAgeBound(t *testing.T, db *sql.DB, f retentionAISessionsFixture) {
	t.Helper()
	if n := retentionAISessionsSessionCount(t, db, f.env1, "exit-old"); n != 0 {
		t.Fatalf("an exited session past the age bound must be deleted, found %d", n)
	}
	if n := retentionAISessionsSessionCount(t, db, f.env1, "exit-young"); n != 1 {
		t.Fatalf("an exited session within the age bound must survive, found %d", n)
	}
	if n := retentionAISessionsSessionCount(t, db, f.env1, "busy-old"); n != 1 {
		t.Fatalf("a non-exit session must survive no matter how old it is, found %d", n)
	}
}

func retentionAISessionsAssertCountCap(t *testing.T, db *sql.DB, f retentionAISessionsFixture) {
	t.Helper()
	if n := retentionAISessionsCount(t, db, `SELECT count(*) FROM ai_sessions WHERE environment_id = $1`, f.envNoisy); n != 500 {
		t.Fatalf("the noisy environment must keep exactly 500 sessions, kept %d", n)
	}
	if n := retentionAISessionsCount(t, db, `SELECT count(*) FROM ai_sessions WHERE environment_id = $1 AND session_id IN ('noisy-501','noisy-502','noisy-503','noisy-504','noisy-505')`, f.envNoisy); n != 0 {
		t.Fatalf("the noisy environment's 5 oldest sessions must be deleted, %d remain", n)
	}
	if n := retentionAISessionsSessionCount(t, db, f.envNoisy, "noisy-1"); n != 1 {
		t.Fatalf("the noisy environment's newest session must survive, found %d", n)
	}

	if n := retentionAISessionsCount(t, db, `SELECT count(*) FROM ai_sessions WHERE environment_id = $1`, f.envQuiet); n != 10 {
		t.Fatalf("a quiet environment in the same tenant as a noisy one must be untouched, has %d of 10", n)
	}
}
