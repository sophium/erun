package backendapi

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/testpg"
)

// End-to-end proof that the comments/releases retention sweep
// (erun-backend-db/retention/comments_releases.sql) deletes exactly the rows
// the design in erun-backend-db/AGENTS.md ("Review Comments And Releases")
// says it should, and nothing else -- against a real migrated postgres:18.3.
// Locks four properties directly against persisted database state:
//
//  1. releases past the 180-day age bound are deleted; releases within it
//     survive.
//  2. releases beyond the 1,000-most-recent-per-tenant count cap are deleted
//     even when every row is well within the age bound.
//  3. comments: an OPEN root thread is exempt from the 30-day age bound no
//     matter how old it is; a CLOSED root thread past the bound is deleted
//     together with every reply on it, even a reply younger than the bound; a
//     CLOSED root thread within the bound survives.
//  4. comments beyond the 5,000-most-recent-closed-root-per-tenant count cap
//     are deleted even when every row is well within the age bound.
//
// It also proves dry_run=true reports the same counts but deletes nothing,
// that both runs record their outcome in retention_runs, and -- driving the
// real retention.sh entrypoint rather than psql directly -- that a plain
// invocation with no ERUN_RETENTION_DRY_RUN set defaults to report-only and
// that a run started while another session holds the advisory lock is
// refused.
//
// The retention SQL is a psql script (:dry_run, \if), so it runs through the
// real psql of a postgres:18.3 container, exactly as retention.sh runs it.
// The sweep is global across tenants and its counts are asserted globally, so
// it runs against a private clone of the package's migrated database: rows
// other tests in this package leave behind can neither inflate the counts nor
// be deleted by the real run.

const (
	retentionCRTenantReleasesAge = "00000000-0000-0000-0000-000000000001"
	retentionCRTenantReleasesCnt = "00000000-0000-0000-0000-000000000002"
	retentionCRTenantCommentsAge = "00000000-0000-0000-0000-000000000003"
	retentionCRTenantCommentsCnt = "00000000-0000-0000-0000-000000000004"
	retentionCRPolicy            = "comments_releases.sql"
)

// retentionCRFixtures is the shared fixture block.
//
// releases-age-tenant (RA): one release 200 days old (past the 180-day bound,
// eligible), one release 10 days old (within it, survives).
// releases-count-tenant (RC): 1005 releases, all seconds old, spaced one
// second apart so ranking by created_at is deterministic. Only the 5 oldest
// (beyond the 1,000-row cap) are eligible.
// comments-age-tenant (CA): an OPEN root 200 days old (exempt regardless of
// age), a CLOSED root 200 days old with a reply posted moments ago (the whole
// thread is eligible, including the young reply), and a CLOSED root 5 days old
// (within the 30-day bound, survives).
// comments-count-tenant (CC): 5005 CLOSED root threads with no replies, all
// seconds old, spaced one second apart. Only the 5 oldest (beyond the 5,000-row
// cap) are eligible.
const retentionCRFixtures = `
INSERT INTO tenants (tenant_id, name) VALUES
  ('00000000-0000-0000-0000-000000000001', 'releases-age-tenant'),
  ('00000000-0000-0000-0000-000000000002', 'releases-count-tenant'),
  ('00000000-0000-0000-0000-000000000003', 'comments-age-tenant'),
  ('00000000-0000-0000-0000-000000000004', 'comments-count-tenant');

INSERT INTO users (user_id, tenant_id, username) VALUES
  ('00000000-0000-0000-0000-000000000103', '00000000-0000-0000-0000-000000000003', 'ca-user'),
  ('00000000-0000-0000-0000-000000000104', '00000000-0000-0000-0000-000000000004', 'cc-user');

INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status) VALUES
  ('00000000-0000-0000-0000-000000000203', '00000000-0000-0000-0000-000000000003', '00000000-0000-0000-0000-000000000103', 'ca-review', 'main', 'feat-ca', 'OPEN'),
  ('00000000-0000-0000-0000-000000000204', '00000000-0000-0000-0000-000000000004', '00000000-0000-0000-0000-000000000104', 'cc-review', 'main', 'feat-cc', 'OPEN');

INSERT INTO releases (tenant_id, target_branch, commit_id, status, version, created_at, updated_at) VALUES
  ('00000000-0000-0000-0000-000000000001', 'main', 'ra-old', 'released', 'v1.0.0', now() - interval '200 days', now() - interval '200 days'),
  ('00000000-0000-0000-0000-000000000001', 'main', 'ra-young', 'released', 'v1.0.1', now() - interval '10 days', now() - interval '10 days');

INSERT INTO releases (tenant_id, target_branch, commit_id, status, version, created_at, updated_at)
SELECT '00000000-0000-0000-0000-000000000002', 'main', 'rc-' || i, 'released', 'v0.0.' || i,
       now() - (i * interval '1 second'), now() - (i * interval '1 second')
FROM generate_series(1, 1005) AS i;

INSERT INTO comments (tenant_id, review_id, creator_user_id, status, parent_comment_id, commit_id, file_path, line, body, created_at, updated_at) VALUES
  ('00000000-0000-0000-0000-000000000003', '00000000-0000-0000-0000-000000000203', '00000000-0000-0000-0000-000000000103', 'OPEN', NULL, 'c', 'ca-open-root', 1, 'open root, never ages out', now() - interval '200 days', now() - interval '200 days'),
  ('00000000-0000-0000-0000-000000000003', '00000000-0000-0000-0000-000000000203', '00000000-0000-0000-0000-000000000103', 'CLOSED', NULL, 'c', 'ca-closed-old-root', 1, 'closed root, past the age bound', now() - interval '200 days', now() - interval '200 days'),
  ('00000000-0000-0000-0000-000000000003', '00000000-0000-0000-0000-000000000203', '00000000-0000-0000-0000-000000000103', 'CLOSED', NULL, 'c', 'ca-closed-young-root', 1, 'closed root, within the age bound', now() - interval '5 days', now() - interval '5 days');

INSERT INTO comments (tenant_id, review_id, creator_user_id, status, parent_comment_id, commit_id, file_path, line, body, created_at, updated_at)
SELECT '00000000-0000-0000-0000-000000000003', '00000000-0000-0000-0000-000000000203', '00000000-0000-0000-0000-000000000103', 'OPEN', comment_id, 'c', 'ca-closed-old-root', 1, 'reply posted moments ago, root is old', now(), now()
FROM comments WHERE tenant_id = '00000000-0000-0000-0000-000000000003' AND file_path = 'ca-closed-old-root' AND parent_comment_id IS NULL;

INSERT INTO comments (tenant_id, review_id, creator_user_id, status, parent_comment_id, commit_id, file_path, line, body, created_at, updated_at)
SELECT '00000000-0000-0000-0000-000000000004', '00000000-0000-0000-0000-000000000204', '00000000-0000-0000-0000-000000000104', 'CLOSED', NULL, 'c', 'cc-' || i, 1, 'closed root ' || i,
       now() - (i * interval '1 second'), now() - (i * interval '1 second')
FROM generate_series(1, 5005) AS i;
`

// TestRetentionCommentsReleasesE2E sweeps comments and releases with the real
// retention policy.
func TestRetentionCommentsReleasesE2E(t *testing.T) {
	ctx := context.Background()
	hostURL, db := retentionCRCloneDatabase(t, ctx)
	h := &retentionCRHarness{t: t, ctx: ctx, db: db, psql: startRetentionPsql(t, ctx, hostURL, retentionCRPolicy)}

	initialReleases, initialComments := h.seed()
	h.dryRun(initialReleases, initialComments)
	h.realRun()
	h.assertReleasesSurvivors()
	h.assertCommentsSurvivors()

	// --- retention.sh itself: default dry_run, and the advisory lock refuses a
	//     second run while one is already in flight ---
	entrypointEnv := []string{"ERUN_DATABASE_URL=" + h.psql.url}
	h.entrypointDefaultsToDryRun(entrypointEnv)
	h.entrypointRefusedWhileLocked(entrypointEnv)
}

// retentionCRHarness carries the cloned database and psql container shared by
// the phases of TestRetentionCommentsReleasesE2E.
type retentionCRHarness struct {
	t    *testing.T
	ctx  context.Context
	db   *sql.DB
	psql retentionPsql
}

func (h *retentionCRHarness) count(query string, args ...any) int {
	h.t.Helper()
	var n int
	mustNoErr(h.t, h.db.QueryRowContext(h.ctx, query, args...).Scan(&n), query)
	return n
}

func (h *retentionCRHarness) expectCount(want int, failure, query string, args ...any) {
	h.t.Helper()
	if got := h.count(query, args...); got != want {
		h.t.Fatalf("%s: got %d, want %d", failure, got, want)
	}
}

func (h *retentionCRHarness) latestRun(table string, dryRun bool, column string) int {
	h.t.Helper()
	return h.count(`SELECT `+column+` FROM retention_runs
			WHERE policy_name = 'comments_releases' AND table_name = $1 AND dry_run = $2
			ORDER BY created_at DESC LIMIT 1`, table, dryRun)
}

func (h *retentionCRHarness) expectRun(table string, dryRun bool, column string, want int, failure string) {
	h.t.Helper()
	if got := h.latestRun(table, dryRun, column); got != want {
		h.t.Fatalf("%s: got %d, want %d", failure, got, want)
	}
}

// seed loads the fixtures and returns the initial release and comment counts.
func (h *retentionCRHarness) seed() (int, int) {
	t := h.t
	t.Helper()
	_, err := h.db.ExecContext(h.ctx, retentionCRFixtures)
	mustNoErr(t, err, "seed retention fixtures")

	initialReleases := h.count(`SELECT count(*) FROM releases`)
	initialComments := h.count(`SELECT count(*) FROM comments`)
	if initialReleases != 1007 {
		t.Fatalf("expected 1007 fixture releases (2 + 1005), got %d", initialReleases)
	}
	if initialComments != 5009 {
		t.Fatalf("expected 5009 fixture comments (3 roots + 1 reply + 5005 roots), got %d", initialComments)
	}
	return initialReleases, initialComments
}

// dryRun: reports the exact eligible counts, deletes nothing, and records its
// outcome with zero deleted.
func (h *retentionCRHarness) dryRun(initialReleases, initialComments int) {
	t := h.t
	t.Helper()
	report, code := h.psql.run(t, h.ctx, nil, "psql", "-v", "ON_ERROR_STOP=1", "-v", "dry_run=true", "-f", retentionPolicyPath(retentionCRPolicy), h.psql.url)
	if code != 0 {
		t.Fatalf("dry run psql exited %d:\n%s", code, report)
	}
	if !regexp.MustCompile(`(?m)^\s*releases\s*\|\s*6\s*$`).MatchString(report) {
		t.Fatalf("dry run must report 6 releases eligible across all tenants (1 age-bound + 5 count-cap), got:\n%s", report)
	}
	if !regexp.MustCompile(`(?m)^\s*comments\s*\|\s*7\s*$`).MatchString(report) {
		t.Fatalf("dry run must report 7 comments eligible across all tenants (the old closed root + its young reply, + 5 count-cap roots), got:\n%s", report)
	}
	h.expectCount(initialReleases, "dry run must not delete any releases", `SELECT count(*) FROM releases`)
	h.expectCount(initialComments, "dry run must not delete any comments", `SELECT count(*) FROM comments`)

	h.expectRun("releases", true, "eligible_count", 6, "the dry run must record 6 releases eligible in retention_runs")
	h.expectRun("releases", true, "deleted_count", 0, "the dry run must record 0 releases deleted in retention_runs")
	h.expectRun("comments", true, "eligible_count", 7, "the dry run must record 7 comments eligible in retention_runs")
	h.expectRun("comments", true, "deleted_count", 0, "the dry run must record 0 comments deleted in retention_runs")
}

// realRun deletes exactly the eligible rows and records matching counts.
func (h *retentionCRHarness) realRun() {
	t := h.t
	t.Helper()
	if out, code := h.psql.run(t, h.ctx, nil, "psql", "-v", "ON_ERROR_STOP=1", "-v", "dry_run=false", "-f", retentionPolicyPath(retentionCRPolicy), h.psql.url); code != 0 {
		t.Fatalf("real run psql exited %d:\n%s", code, out)
	}

	h.expectRun("releases", false, "eligible_count", 6, "the real run must record 6 releases eligible in retention_runs")
	h.expectRun("releases", false, "deleted_count", 6, "the real run must record 6 releases deleted in retention_runs")
	h.expectRun("comments", false, "eligible_count", 7, "the real run must record 7 comments eligible in retention_runs")
	h.expectRun("comments", false, "deleted_count", 7, "the real run must record 7 comments deleted in retention_runs")
}

func (h *retentionCRHarness) assertReleasesSurvivors() {
	h.t.Helper()
	// 1. releases-age-tenant: the 200-day-old row is gone, the 10-day-old row survives.
	h.expectCount(0, "the age-bound tenant's old release must be deleted",
		`SELECT count(*) FROM releases WHERE tenant_id = $1 AND commit_id = 'ra-old'`, retentionCRTenantReleasesAge)
	h.expectCount(1, "the age-bound tenant's young release must survive",
		`SELECT count(*) FROM releases WHERE tenant_id = $1 AND commit_id = 'ra-young'`, retentionCRTenantReleasesAge)

	// 2. releases-count-tenant: exactly 1,000 survive -- the 5 oldest are gone.
	h.expectCount(1000, "the count-cap tenant must keep exactly 1000 releases",
		`SELECT count(*) FROM releases WHERE tenant_id = $1`, retentionCRTenantReleasesCnt)
	h.expectCount(0, "the count-cap tenant's 5 oldest releases must be deleted",
		`SELECT count(*) FROM releases WHERE tenant_id = $1 AND commit_id IN ('rc-1001','rc-1002','rc-1003','rc-1004','rc-1005')`, retentionCRTenantReleasesCnt)
	h.expectCount(1, "the count-cap tenant's newest release must survive",
		`SELECT count(*) FROM releases WHERE tenant_id = $1 AND commit_id = 'rc-1'`, retentionCRTenantReleasesCnt)
}

func (h *retentionCRHarness) assertCommentsSurvivors() {
	h.t.Helper()
	// 3. comments-age-tenant: OPEN root survives regardless of age, CLOSED-old
	//    root and its reply are both gone, CLOSED-young root survives.
	h.expectCount(1, "an OPEN root must survive no matter how old it is",
		`SELECT count(*) FROM comments WHERE tenant_id = $1 AND file_path = 'ca-open-root'`, retentionCRTenantCommentsAge)
	h.expectCount(0, "a CLOSED root past the age bound must be deleted",
		`SELECT count(*) FROM comments WHERE tenant_id = $1 AND file_path = 'ca-closed-old-root'`, retentionCRTenantCommentsAge)
	h.expectCount(0, "a reply on a deleted thread must be deleted too, even if it is younger than the age bound",
		`SELECT count(*) FROM comments WHERE parent_comment_id IN (SELECT comment_id FROM comments WHERE tenant_id = $1 AND file_path = 'ca-closed-old-root')`, retentionCRTenantCommentsAge)
	h.expectCount(1, "a CLOSED root within the age bound must survive",
		`SELECT count(*) FROM comments WHERE tenant_id = $1 AND file_path = 'ca-closed-young-root'`, retentionCRTenantCommentsAge)

	// 4. comments-count-tenant: exactly 5,000 closed roots survive -- the 5 oldest are gone.
	h.expectCount(5000, "the count-cap tenant must keep exactly 5000 comments",
		`SELECT count(*) FROM comments WHERE tenant_id = $1`, retentionCRTenantCommentsCnt)
	h.expectCount(0, "the count-cap tenant's 5 oldest closed roots must be deleted",
		`SELECT count(*) FROM comments WHERE tenant_id = $1 AND file_path IN ('cc-5001','cc-5002','cc-5003','cc-5004','cc-5005')`, retentionCRTenantCommentsCnt)
	h.expectCount(1, "the count-cap tenant's newest closed root must survive",
		`SELECT count(*) FROM comments WHERE tenant_id = $1 AND file_path = 'cc-1'`, retentionCRTenantCommentsCnt)
}

// entrypointDefaultsToDryRun: a plain invocation with ERUN_RETENTION_DRY_RUN
// unset must default to report-only and delete nothing.
func (h *retentionCRHarness) entrypointDefaultsToDryRun(entrypointEnv []string) {
	t := h.t
	t.Helper()
	beforeReleaseCount := h.count(`SELECT count(*) FROM releases`)
	if out, code := h.psql.run(t, h.ctx, entrypointEnv, retentionContainerEntrypoint); code != 0 {
		t.Fatalf("retention.sh must succeed with no ERUN_RETENTION_DRY_RUN set: exit %d:\n%s", code, out)
	}
	h.expectCount(beforeReleaseCount, "retention.sh with no ERUN_RETENTION_DRY_RUN set must default to dry_run=true and delete nothing",
		`SELECT count(*) FROM releases`)
	var lastDryRun bool
	mustNoErr(t, h.db.QueryRowContext(h.ctx, `SELECT dry_run FROM retention_runs
		WHERE policy_name = 'comments_releases' ORDER BY created_at DESC LIMIT 1`).Scan(&lastDryRun), "read latest retention run")
	if !lastDryRun {
		t.Fatal("retention.sh's default run must record dry_run=true in retention_runs")
	}
}

// entrypointRefusedWhileLocked holds the advisory lock in another session and
// confirms a concurrent retention.sh run is refused. The shell script held it
// from a background psql for a fixed pg_sleep(5); a session of this test's own
// holds it until explicitly released instead, so neither side races a timer.
func (h *retentionCRHarness) entrypointRefusedWhileLocked(entrypointEnv []string) {
	t, ctx := h.t, h.ctx
	t.Helper()
	holder, err := h.db.Conn(ctx)
	mustNoErr(t, err, "open the advisory lock holder session")
	t.Cleanup(func() { _ = holder.Close() })
	_, err = holder.ExecContext(ctx, `SELECT pg_advisory_lock(hashtext('erun_retention'))`)
	mustNoErr(t, err, "background session never acquired the advisory lock")

	out, code := h.psql.run(t, ctx, entrypointEnv, retentionContainerEntrypoint)
	if code == 0 {
		t.Fatalf("retention.sh must refuse to run while the advisory lock is held by another session, got exit 0:\n%s", out)
	}
	if !strings.Contains(out, "advisory lock held") {
		t.Fatalf("retention.sh's refusal must name the advisory lock, got: %s", out)
	}

	var released bool
	mustNoErr(t, holder.QueryRowContext(ctx, `SELECT pg_advisory_unlock(hashtext('erun_retention'))`).Scan(&released), "release the advisory lock")
	if !released {
		t.Fatal("the holder session did not hold the advisory lock it took")
	}
	mustNoErr(t, holder.Close(), "close the advisory lock holder session")

	if out, code := h.psql.run(t, ctx, entrypointEnv, retentionContainerEntrypoint); code != 0 {
		t.Fatalf("retention.sh must succeed once the advisory lock is released: exit %d:\n%s", code, out)
	}
}

// retentionCRCloneDatabase creates a private copy of the package's migrated
// database for this test and returns its host-side URL and an open handle.
// CREATE DATABASE ... TEMPLATE needs the template idle; the package's tests
// run serially and close their pools, so a short retry covers backends still
// exiting after an earlier test's cleanup.
func retentionCRCloneDatabase(t *testing.T, ctx context.Context) (string, *sql.DB) {
	t.Helper()
	fresh := testpg.FreshURL(t)
	db, err := sql.Open("pgx", fresh)
	mustNoErr(t, err, "open the fresh database")
	t.Cleanup(func() { _ = db.Close() })
	mustNoErr(t, db.PingContext(ctx), "connect to the fresh database")
	return fresh, db
}
