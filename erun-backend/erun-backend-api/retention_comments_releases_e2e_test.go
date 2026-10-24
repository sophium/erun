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

// Runs erun-backend-db/retention/comments_releases.sql through the real psql, against
// a private database clone because the sweep and its counts are global.

const (
	retentionCRTenantReleasesAge = "00000000-0000-0000-0000-000000000001"
	retentionCRTenantReleasesCnt = "00000000-0000-0000-0000-000000000002"
	retentionCRTenantCommentsAge = "00000000-0000-0000-0000-000000000003"
	retentionCRTenantCommentsCnt = "00000000-0000-0000-0000-000000000004"
	retentionCRPolicy            = "comments_releases.sql"
)

// Each tenant isolates one property; rows are spaced one second apart so ranking by
// created_at is deterministic.
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

func TestRetentionCommentsReleasesE2E(t *testing.T) {
	ctx := context.Background()
	hostURL, db := retentionCRCloneDatabase(t, ctx)
	h := &retentionCRHarness{t: t, ctx: ctx, db: db, psql: startRetentionPsql(t, ctx, hostURL, retentionCRPolicy)}

	initialReleases, initialComments := h.seed()
	h.dryRun(initialReleases, initialComments)
	h.realRun()
	h.assertReleasesSurvivors()
	h.assertCommentsSurvivors()

	entrypointEnv := []string{"ERUN_DATABASE_URL=" + h.psql.url}
	h.entrypointDefaultsToDryRun(entrypointEnv)
	h.entrypointRefusedWhileLocked(entrypointEnv)
}

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
	h.expectCount(0, "the age-bound tenant's old release must be deleted",
		`SELECT count(*) FROM releases WHERE tenant_id = $1 AND commit_id = 'ra-old'`, retentionCRTenantReleasesAge)
	h.expectCount(1, "the age-bound tenant's young release must survive",
		`SELECT count(*) FROM releases WHERE tenant_id = $1 AND commit_id = 'ra-young'`, retentionCRTenantReleasesAge)

	h.expectCount(1000, "the count-cap tenant must keep exactly 1000 releases",
		`SELECT count(*) FROM releases WHERE tenant_id = $1`, retentionCRTenantReleasesCnt)
	h.expectCount(0, "the count-cap tenant's 5 oldest releases must be deleted",
		`SELECT count(*) FROM releases WHERE tenant_id = $1 AND commit_id IN ('rc-1001','rc-1002','rc-1003','rc-1004','rc-1005')`, retentionCRTenantReleasesCnt)
	h.expectCount(1, "the count-cap tenant's newest release must survive",
		`SELECT count(*) FROM releases WHERE tenant_id = $1 AND commit_id = 'rc-1'`, retentionCRTenantReleasesCnt)
}

func (h *retentionCRHarness) assertCommentsSurvivors() {
	h.t.Helper()
	h.expectCount(1, "an OPEN root must survive no matter how old it is",
		`SELECT count(*) FROM comments WHERE tenant_id = $1 AND file_path = 'ca-open-root'`, retentionCRTenantCommentsAge)
	h.expectCount(0, "a CLOSED root past the age bound must be deleted",
		`SELECT count(*) FROM comments WHERE tenant_id = $1 AND file_path = 'ca-closed-old-root'`, retentionCRTenantCommentsAge)
	h.expectCount(0, "a reply on a deleted thread must be deleted too, even if it is younger than the age bound",
		`SELECT count(*) FROM comments WHERE parent_comment_id IN (SELECT comment_id FROM comments WHERE tenant_id = $1 AND file_path = 'ca-closed-old-root')`, retentionCRTenantCommentsAge)
	h.expectCount(1, "a CLOSED root within the age bound must survive",
		`SELECT count(*) FROM comments WHERE tenant_id = $1 AND file_path = 'ca-closed-young-root'`, retentionCRTenantCommentsAge)

	h.expectCount(5000, "the count-cap tenant must keep exactly 5000 comments",
		`SELECT count(*) FROM comments WHERE tenant_id = $1`, retentionCRTenantCommentsCnt)
	h.expectCount(0, "the count-cap tenant's 5 oldest closed roots must be deleted",
		`SELECT count(*) FROM comments WHERE tenant_id = $1 AND file_path IN ('cc-5001','cc-5002','cc-5003','cc-5004','cc-5005')`, retentionCRTenantCommentsCnt)
	h.expectCount(1, "the count-cap tenant's newest closed root must survive",
		`SELECT count(*) FROM comments WHERE tenant_id = $1 AND file_path = 'cc-1'`, retentionCRTenantCommentsCnt)
}

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

// A session holds the advisory lock until released, so neither side races a timer.
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

// CREATE DATABASE ... TEMPLATE needs the template idle; the retry covers backends
// still exiting from an earlier test.
func retentionCRCloneDatabase(t *testing.T, ctx context.Context) (string, *sql.DB) {
	t.Helper()
	fresh := testpg.FreshURL(t)
	db, err := sql.Open("pgx", fresh)
	mustNoErr(t, err, "open the fresh database")
	t.Cleanup(func() { _ = db.Close() })
	mustNoErr(t, db.PingContext(ctx), "connect to the fresh database")
	return fresh, db
}
