package backendapi

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/testpg"
)

// End-to-end proof that the invites/invite_requests retention sweep
// (erun-backend-db/retention/invites_invite_requests.sql) deletes exactly the
// rows the design in erun-backend-db/AGENTS.md ("Invites And Invite
// Requests") says it should, and nothing else -- against the real migrated
// postgres testpg serves. Ported from
// erun-devops/docker/erun-backend-db/retention_invites_test.sh. Locks:
//
//  1. invite_requests: a PENDING request is exempt regardless of age; an
//     APPROVED/DECLINED request past the 180-day age bound (on updated_at)
//     is deleted; one within it survives.
//  2. invites: a live (unconsumed, unexpired) invite is exempt; a consumed
//     invite past the 365-day bound (on consumed_at) is deleted; one within
//     it survives; an unconsumed expired invite past the 30-day bound (on
//     expires_at) is deleted; one within it survives; a population beyond the
//     10,000-per-tenant count cap is deleted even within its age bound.
//  3. a consumed invite still referenced by invite_requests.minted_invite_id
//     survives even past its own age bound -- the RESTRICT-FK guard.
//
// It also proves dry_run=true reports the same counts but deletes nothing,
// and that both runs record their outcome in retention_runs.
//
// The SQL file runs through retention.sh itself in a real psql container.

const retentionInvitesPolicy = "invites_invite_requests.sql"

// retentionInvitesUnlocked matches retention.sh's final pg_advisory_unlock
// result printed true.
var retentionInvitesUnlocked = regexp.MustCompile(`(?m)^\s*pg_advisory_unlock\s*\n-+\n\s*t\s*$`)

// retentionInvitesRun runs the sweep and returns the per-table
// eligible_for_deletion report the file's SELECTs print.
func retentionInvitesRun(t *testing.T, psql retentionPsql, dryRun bool) map[string]int64 {
	t.Helper()
	out := psql.sweep(t, context.Background(), dryRun)
	if !retentionInvitesUnlocked.MatchString(out) {
		t.Fatalf("the retention advisory lock was not held at the end of the sweep:\n%s", out)
	}
	report := map[string]int64{}
	for table, n := range retentionReport(out) {
		count, err := strconv.ParseInt(n, 10, 64)
		mustNoErr(t, err, "parse retention report")
		report[table] = count
	}
	return report
}

func retentionInvitesCount(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	mustNoErr(t, db.QueryRow(query, args...).Scan(&n), "count: "+query)
	return n
}

func retentionInvitesExpect(t *testing.T, db *sql.DB, want int64, why, query string, args ...any) {
	t.Helper()
	if got := retentionInvitesCount(t, db, query, args...); got != want {
		t.Fatalf("%s: got %d, want %d (%s)", why, got, want, query)
	}
}

const retentionInvitesRunQuery = `SELECT %s FROM retention_runs WHERE policy_name = 'invites_invite_requests' AND table_name = $1 AND dry_run = $2 ORDER BY created_at DESC LIMIT 1`

func TestRetentionInvitesInviteRequestsSweep(t *testing.T) {
	databaseURL := testpg.URL(t)
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	psql := startRetentionPsql(t, context.Background(), databaseURL, retentionInvitesPolicy)

	retentionInvitesRequireNoFixtures(t, db)
	t2 := retentionInvitesSeed(t, db)
	initialInvites, initialRequests := retentionInvitesInitialCounts(t, db)

	retentionInvitesCheckDryRun(t, db, psql, initialInvites, initialRequests)
	retentionInvitesCheckRealRun(t, db, psql)
	retentionInvitesCheckRequestsOutcome(t, db)
	retentionInvitesCheckInvitesOutcome(t, db)
	retentionInvitesCheckCountCap(t, db, t2)
}

// retentionInvitesRequireNoFixtures guards the shared database: the sweep's
// counts are platform-wide for invite_requests and its delete is global, so it
// must hold no other invite fixtures this sweep would count.
func retentionInvitesRequireNoFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	if n := retentionInvitesCount(t, db, `SELECT count(*) FROM invites`); n != 0 {
		t.Fatalf("expected no pre-existing invites in the shared test database, found %d", n)
	}
	if n := retentionInvitesCount(t, db, `SELECT count(*) FROM invite_requests`); n != 0 {
		t.Fatalf("expected no pre-existing invite_requests in the shared test database, found %d", n)
	}
}

// retentionInvitesSeed seeds tenants, users and every fixture, registers their
// cleanup, and returns the count-cap tenant's id.
func retentionInvitesSeed(t *testing.T, db *sql.DB) string {
	t.Helper()
	// Unique names so a rerun against ERUN_TEST_DATABASE_URL never collides.
	suffix := time.Now().Format("20060102150405.000000")
	var t1, t2, u1, u2 string
	mustNoErr(t, db.QueryRow(`INSERT INTO tenants (name) VALUES ($1) RETURNING tenant_id`, "t1-"+suffix).Scan(&t1), "seed t1")
	mustNoErr(t, db.QueryRow(`INSERT INTO tenants (name) VALUES ($1) RETURNING tenant_id`, "invites-count-tenant-"+suffix).Scan(&t2), "seed invites-count-tenant")
	t.Cleanup(func() { retentionInvitesCleanup(t, db, t1, t2) })
	mustNoErr(t, db.QueryRow(`INSERT INTO users (tenant_id, username) VALUES ($1, 'u1') RETURNING user_id`, t1).Scan(&u1), "seed u1")
	mustNoErr(t, db.QueryRow(`INSERT INTO users (tenant_id, username) VALUES ($1, 'u2') RETURNING user_id`, t2).Scan(&u2), "seed u2")

	guardedInviteID := retentionInvitesSeedInvites(t, db, t1, u1)
	retentionInvitesSeedRequests(t, db, "t1-"+suffix, guardedInviteID, u1)
	retentionInvitesSeedCountCap(t, db, t2, u2)
	return t2
}

func retentionInvitesCleanup(t *testing.T, db *sql.DB, t1, t2 string) {
	t.Helper()
	for _, q := range []string{
		`DELETE FROM invite_requests WHERE issuer = 'iss'`,
		`DELETE FROM invites WHERE tenant_id IN ($1, $2)`,
		`DELETE FROM users WHERE tenant_id IN ($1, $2)`,
		`DELETE FROM tenants WHERE tenant_id IN ($1, $2)`,
	} {
		var err error
		if strings.Contains(q, "$1") {
			_, err = db.Exec(q, t1, t2)
		} else {
			_, err = db.Exec(q)
		}
		if err != nil {
			t.Logf("cleanup %q: %v", q, err)
		}
	}
}

// retentionInvitesSeedInvites seeds the per-case invites (retention_invites_test.sh
// lines 107-147) and returns the guarded invite's id.
func retentionInvitesSeedInvites(t *testing.T, db *sql.DB, t1, u1 string) string {
	t.Helper()
	var guardedInviteID string
	_, err := db.Exec(`
INSERT INTO invites (tenant_id, created_by_user_id, issuer, token, email, expires_at, consumed_at, created_at, updated_at) VALUES
  ($1, $2, 'iss', 'tok-consumed-old', 'a@x.com', now() - interval '399 days', now() - interval '400 days', now() - interval '401 days', now() - interval '400 days'),
  ($1, $2, 'iss', 'tok-consumed-young', 'b@x.com', now() - interval '9 days', now() - interval '10 days', now() - interval '11 days', now() - interval '10 days'),
  ($1, $2, 'iss', 'tok-expired-old', 'c@x.com', now() - interval '40 days', NULL, now() - interval '50 days', now() - interval '50 days'),
  ($1, $2, 'iss', 'tok-expired-young', 'd@x.com', now() - interval '5 days', NULL, now() - interval '10 days', now() - interval '10 days'),
  ($1, $2, 'iss', 'tok-live', 'e@x.com', now() + interval '10 days', NULL, now(), now()),
  ($1, $2, 'iss', 'tok-consumed-guarded', 'f@x.com', now() - interval '399 days', now() - interval '400 days', now() - interval '401 days', now() - interval '400 days')`,
		t1, u1)
	mustNoErr(t, err, "seed t1 invites")
	mustNoErr(t, db.QueryRow(`SELECT invite_id FROM invites WHERE token = 'tok-consumed-guarded'`).Scan(&guardedInviteID), "look up guarded invite")
	return guardedInviteID
}

func retentionInvitesSeedRequests(t *testing.T, db *sql.DB, tenantName, guardedInviteID, u1 string) {
	t.Helper()
	_, err := db.Exec(`
INSERT INTO invite_requests (issuer, subject, kind, tenant_name, status, minted_invite_id, decided_by_user_id, decline_reason, updated_at) VALUES
  ('iss', 'sub-pending', 'JOIN_TENANT', $1, 'PENDING', $2, NULL, NULL, now() - interval '400 days'),
  ('iss', 'sub-approved-old', 'JOIN_TENANT', $1, 'APPROVED', NULL, $3, NULL, now() - interval '400 days'),
  ('iss', 'sub-declined-young', 'JOIN_TENANT', $1, 'DECLINED', NULL, $3, 'no capacity', now() - interval '10 days')`,
		tenantName, guardedInviteID, u1)
	mustNoErr(t, err, "seed invite_requests")
}

func retentionInvitesSeedCountCap(t *testing.T, db *sql.DB, t2, u2 string) {
	t.Helper()
	_, err := db.Exec(`
INSERT INTO invites (tenant_id, created_by_user_id, issuer, token, expires_at, consumed_at, created_at, updated_at)
SELECT $1, $2, 'iss', 'count-' || i,
       now() + interval '1 day', now() - (i * interval '1 second'), now() - (i * interval '1 second'), now() - (i * interval '1 second')
FROM generate_series(1, 10005) AS i`, t2, u2)
	mustNoErr(t, err, "seed count-cap invites")
}

// lines 153-156
func retentionInvitesInitialCounts(t *testing.T, db *sql.DB) (invites, requests int64) {
	t.Helper()
	initialInvites := retentionInvitesCount(t, db, `SELECT count(*) FROM invites`)
	initialRequests := retentionInvitesCount(t, db, `SELECT count(*) FROM invite_requests`)
	if initialInvites != 10011 {
		t.Fatalf("expected 10011 fixture invites (6 + 10005 count-cap), got %d", initialInvites)
	}
	if initialRequests != 3 {
		t.Fatalf("expected 3 fixture invite_requests, got %d", initialRequests)
	}
	return initialInvites, initialRequests
}

func retentionInvitesCheckDryRun(t *testing.T, db *sql.DB, psql retentionPsql, initialInvites, initialRequests int64) {
	t.Helper()
	runQuery := retentionInvitesRunQuery

	// --- Dry run reports the exact eligible counts and deletes nothing (lines 154-163) ---
	report := retentionInvitesRun(t, psql, true)
	if got, ok := report["invite_requests"]; !ok || got != 1 {
		t.Fatalf("dry run must report 1 invite_requests eligible (sub-approved-old), got %v", report)
	}
	if got, ok := report["invites"]; !ok || got != 7 {
		t.Fatalf("dry run must report 7 invites eligible (consumed-old + expired-old + 5 count-cap; the guarded consumed invite must NOT be counted), got %v", report)
	}
	retentionInvitesExpect(t, db, initialInvites, "dry run must not delete any invites", `SELECT count(*) FROM invites`)
	retentionInvitesExpect(t, db, initialRequests, "dry run must not delete any invite_requests", `SELECT count(*) FROM invite_requests`)

	// --- The dry run recorded its outcome: eligible counts, zero deleted (lines 165-173) ---
	retentionInvitesExpect(t, db, 1, "the dry run must record 1 invite_requests eligible in retention_runs", fmt.Sprintf(runQuery, "eligible_count"), "invite_requests", true)
	retentionInvitesExpect(t, db, 0, "the dry run must record 0 invite_requests deleted in retention_runs", fmt.Sprintf(runQuery, "deleted_count"), "invite_requests", true)
	retentionInvitesExpect(t, db, 7, "the dry run must record 7 invites eligible in retention_runs", fmt.Sprintf(runQuery, "eligible_count"), "invites", true)
	retentionInvitesExpect(t, db, 0, "the dry run must record 0 invites deleted in retention_runs", fmt.Sprintf(runQuery, "deleted_count"), "invites", true)
}

func retentionInvitesCheckRealRun(t *testing.T, db *sql.DB, psql retentionPsql) {
	t.Helper()
	runQuery := retentionInvitesRunQuery

	// --- Real run deletes exactly the eligible rows (line 176) ---
	retentionInvitesRun(t, psql, false)

	// --- The real run recorded its outcome (lines 178-186) ---
	retentionInvitesExpect(t, db, 1, "the real run must record 1 invite_requests eligible in retention_runs", fmt.Sprintf(runQuery, "eligible_count"), "invite_requests", false)
	retentionInvitesExpect(t, db, 1, "the real run must record 1 invite_requests deleted in retention_runs", fmt.Sprintf(runQuery, "deleted_count"), "invite_requests", false)
	retentionInvitesExpect(t, db, 7, "the real run must record 7 invites eligible in retention_runs", fmt.Sprintf(runQuery, "eligible_count"), "invites", false)
	retentionInvitesExpect(t, db, 7, "the real run must record 7 invites deleted in retention_runs", fmt.Sprintf(runQuery, "deleted_count"), "invites", false)
}

// 1. invite_requests (lines 188-195)
func retentionInvitesCheckRequestsOutcome(t *testing.T, db *sql.DB) {
	t.Helper()
	retentionInvitesExpect(t, db, 1, "a PENDING request must survive no matter how old it is", `SELECT count(*) FROM invite_requests WHERE subject = 'sub-pending'`)
	retentionInvitesExpect(t, db, 0, "an APPROVED request past the age bound must be deleted", `SELECT count(*) FROM invite_requests WHERE subject = 'sub-approved-old'`)
	retentionInvitesExpect(t, db, 1, "a DECLINED request within the age bound must survive", `SELECT count(*) FROM invite_requests WHERE subject = 'sub-declined-young'`)
}

func retentionInvitesCheckInvitesOutcome(t *testing.T, db *sql.DB) {
	t.Helper()
	// 2. invites (lines 197-208)
	retentionInvitesExpect(t, db, 0, "a consumed invite past the 365-day bound must be deleted", `SELECT count(*) FROM invites WHERE token = 'tok-consumed-old'`)
	retentionInvitesExpect(t, db, 1, "a consumed invite within the 365-day bound must survive", `SELECT count(*) FROM invites WHERE token = 'tok-consumed-young'`)
	retentionInvitesExpect(t, db, 0, "an unconsumed expired invite past the 30-day bound must be deleted", `SELECT count(*) FROM invites WHERE token = 'tok-expired-old'`)
	retentionInvitesExpect(t, db, 1, "an unconsumed expired invite within the 30-day bound must survive", `SELECT count(*) FROM invites WHERE token = 'tok-expired-young'`)
	retentionInvitesExpect(t, db, 1, "a live invite must survive regardless of the sweep", `SELECT count(*) FROM invites WHERE token = 'tok-live'`)

	// 3. minted_invite_id guard (lines 210-214)
	retentionInvitesExpect(t, db, 1, "a consumed invite still referenced by invite_requests.minted_invite_id must survive", `SELECT count(*) FROM invites WHERE token = 'tok-consumed-guarded'`)
}

// 4. count cap (lines 216-222)
func retentionInvitesCheckCountCap(t *testing.T, db *sql.DB, t2 string) {
	t.Helper()
	retentionInvitesExpect(t, db, 10000, "the count-cap tenant must keep exactly 10000 invites", `SELECT count(*) FROM invites WHERE tenant_id = $1`, t2)
	retentionInvitesExpect(t, db, 0, "the count-cap tenant's 5 oldest invites must be deleted", `SELECT count(*) FROM invites WHERE tenant_id = $1 AND token IN ('count-10001','count-10002','count-10003','count-10004','count-10005')`, t2)
	retentionInvitesExpect(t, db, 1, "the count-cap tenant's newest invite must survive", `SELECT count(*) FROM invites WHERE tenant_id = $1 AND token = 'count-1'`, t2)
}
