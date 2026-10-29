package backendapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/testpg"
)

// Runs erun-backend-db/retention/reviews.sql through retention.sh in a real psql
// container; policy is in erun-backend-db/AGENTS.md ("Reviews"). The design's guard
// list omits builds, so the referential order is locked here directly. Ids and tenant
// names are run-unique because the package database is shared.

const retentionReviewsPolicy = "reviews.sql"

// {{p}} replaces the id prefix and {{n}} suffixes tenant names.
const retentionReviewsFixtureSQL = `
INSERT INTO tenants (tenant_id, name) VALUES
  ('{{p}}000000000001', 'clean{{n}}'),
  ('{{p}}000000000002', 'builds-unpinned{{n}}'),
  ('{{p}}000000000003', 'builds-pinned{{n}}'),
  ('{{p}}000000000004', 'comments-guard{{n}}'),
  ('{{p}}000000000005', 'releases-guard{{n}}'),
  ('{{p}}000000000006', 'legacy-gate-build-guard{{n}}'),
  ('{{p}}000000000007', 'queue-guard{{n}}'),
  ('{{p}}000000000008', 'open{{n}}'),
  ('{{p}}000000000009', 'merged{{n}}'),
  ('{{p}}00000000000a', 'young{{n}}'),
  ('{{p}}00000000000b', 'count-cap{{n}}');

INSERT INTO users (user_id, tenant_id, username) VALUES
  ('{{p}}100000000001', '{{p}}000000000001', 'u1'),
  ('{{p}}100000000002', '{{p}}000000000002', 'u2'),
  ('{{p}}100000000003', '{{p}}000000000003', 'u3'),
  ('{{p}}100000000004', '{{p}}000000000004', 'u4'),
  ('{{p}}100000000005', '{{p}}000000000005', 'u5'),
  ('{{p}}100000000006', '{{p}}000000000006', 'u6'),
  ('{{p}}100000000007', '{{p}}000000000007', 'u7'),
  ('{{p}}100000000008', '{{p}}000000000008', 'u8'),
  ('{{p}}100000000009', '{{p}}000000000009', 'u9'),
  ('{{p}}10000000000a', '{{p}}00000000000a', 'ua'),
  ('{{p}}10000000000b', '{{p}}00000000000b', 'ub');

-- 1: clean -- CLOSED, 200 days old, no referencing rows.
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status, created_at, updated_at) VALUES
  ('{{p}}000000000101', '{{p}}000000000001', '{{p}}100000000001', 'r', 'main', 'feat', 'CLOSED', now() - interval '200 days', now() - interval '200 days');
INSERT INTO review_reviewers (tenant_id, review_id, user_id) VALUES
  ('{{p}}000000000001', '{{p}}000000000101', '{{p}}100000000001');

-- 2: builds-unpinned -- CLOSED, 200 days old, an unpinned successful build
-- still references it via builds.review_id alone.
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status, created_at, updated_at) VALUES
  ('{{p}}000000000201', '{{p}}000000000002', '{{p}}100000000002', 'r', 'main', 'feat', 'CLOSED', now() - interval '200 days', now() - interval '200 days');
INSERT INTO review_reviewers (tenant_id, review_id, user_id) VALUES
  ('{{p}}000000000002', '{{p}}000000000201', '{{p}}100000000002');
INSERT INTO builds (build_id, tenant_id, review_id, kind, successful, commit_id, version) VALUES
  ('{{p}}000000000202', '{{p}}000000000002', '{{p}}000000000201', 'RECORDED', true, 'c', '1.0.0');

-- 3: builds-pinned -- starts OPEN so a failed build can reference it,
-- then backdated to CLOSED with the build pinned as last_failed_build_id.
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status) VALUES
  ('{{p}}000000000301', '{{p}}000000000003', '{{p}}100000000003', 'r', 'main', 'feat', 'OPEN');
INSERT INTO review_reviewers (tenant_id, review_id, user_id) VALUES
  ('{{p}}000000000003', '{{p}}000000000301', '{{p}}100000000003');
INSERT INTO builds (build_id, tenant_id, review_id, kind, successful, commit_id, version, failure_detail) VALUES
  ('{{p}}000000000302', '{{p}}000000000003', '{{p}}000000000301', 'RECORDED', false, 'c', '1.0.0', 'build failed');
ALTER TABLE reviews DISABLE TRIGGER reviews_set_timestamps;
UPDATE reviews SET status = 'CLOSED', last_failed_build_id = '{{p}}000000000302',
       created_at = now() - interval '200 days', updated_at = now() - interval '200 days'
  WHERE review_id = '{{p}}000000000301';
ALTER TABLE reviews ENABLE TRIGGER reviews_set_timestamps;

-- 4: comments-guard -- CLOSED, 200 days old, referenced by a comment.
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status, created_at, updated_at) VALUES
  ('{{p}}000000000401', '{{p}}000000000004', '{{p}}100000000004', 'r', 'main', 'feat', 'CLOSED', now() - interval '200 days', now() - interval '200 days');
INSERT INTO comments (tenant_id, review_id, creator_user_id, status, commit_id, file_path, line, body) VALUES
  ('{{p}}000000000004', '{{p}}000000000401', '{{p}}100000000004', 'CLOSED', 'c', 'f', 1, 'still on the books');

-- 5: releases-guard -- CLOSED, 200 days old, referenced by a release.
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status, created_at, updated_at) VALUES
  ('{{p}}000000000501', '{{p}}000000000005', '{{p}}100000000005', 'r', 'main', 'feat', 'CLOSED', now() - interval '200 days', now() - interval '200 days');
INSERT INTO releases (tenant_id, review_id, target_branch, commit_id, status, version) VALUES
  ('{{p}}000000000005', '{{p}}000000000501', 'main', 'c', 'released', 'v1');

-- 6: legacy-gate-build-guard -- CLOSED, 200 days old, referenced only by a
-- historical GATE build row, a kind no longer written but still on the books.
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status, created_at, updated_at) VALUES
  ('{{p}}000000000601', '{{p}}000000000006', '{{p}}100000000006', 'r', 'main', 'feat', 'CLOSED', now() - interval '200 days', now() - interval '200 days');
INSERT INTO builds (build_id, tenant_id, review_id, kind, successful, commit_id, version, failure_detail) VALUES
  ('{{p}}000000000602', '{{p}}000000000006', '{{p}}000000000601', 'GATE', false, 'c', NULL, 'gate failed');

-- 7: queue-guard -- CLOSED, 200 days old, referenced by a review_merge_queue row.
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status, created_at, updated_at) VALUES
  ('{{p}}000000000701', '{{p}}000000000007', '{{p}}100000000007', 'r', 'main', 'feat', 'CLOSED', now() - interval '200 days', now() - interval '200 days');
INSERT INTO review_merge_queue (tenant_id, target_branch, review_id) VALUES
  ('{{p}}000000000007', 'main', '{{p}}000000000701');

-- 8: open -- OPEN, 200 days old, no referencing rows.
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status, created_at, updated_at) VALUES
  ('{{p}}000000000801', '{{p}}000000000008', '{{p}}100000000008', 'r', 'main', 'feat', 'OPEN', now() - interval '200 days', now() - interval '200 days');

-- 9: merged -- starts OPEN so a historical successful GATE build can
-- reference it, then backdated to MERGED with the build pinned as
-- last_merged_build_id.
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status) VALUES
  ('{{p}}000000000901', '{{p}}000000000009', '{{p}}100000000009', 'r', 'main', 'feat', 'OPEN');
INSERT INTO builds (build_id, tenant_id, review_id, kind, successful, commit_id, version) VALUES
  ('{{p}}000000000902', '{{p}}000000000009', '{{p}}000000000901', 'GATE', true, 'c', NULL);
ALTER TABLE reviews DISABLE TRIGGER reviews_set_timestamps;
UPDATE reviews SET status = 'MERGED', last_merged_build_id = '{{p}}000000000902',
       created_at = now() - interval '200 days', updated_at = now() - interval '200 days'
  WHERE review_id = '{{p}}000000000901';
ALTER TABLE reviews ENABLE TRIGGER reviews_set_timestamps;

-- 10 (tenant a): young -- CLOSED, 5 days old, no referencing rows.
INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status, created_at, updated_at) VALUES
  ('{{p}}000000000a01', '{{p}}00000000000a', '{{p}}10000000000a', 'r', 'main', 'feat', 'CLOSED', now() - interval '5 days', now() - interval '5 days');

-- 11 (tenant b): count-cap -- 2005 CLOSED reviews, no referencing rows,
-- seconds apart. Only the 5 oldest (beyond the 2,000-row cap) are eligible.
INSERT INTO reviews (tenant_id, author_user_id, name, target_branch, source_branch, status, created_at, updated_at)
SELECT '{{p}}00000000000b', '{{p}}10000000000b', 'r-' || i, 'main', 'feat-' || i, 'CLOSED',
       now() - (i * interval '1 second'), now() - (i * interval '1 second')
FROM generate_series(1, 2005) AS i;
`

type retentionReviewsFixture struct {
	db        *sql.DB
	psql      retentionPsql
	prefix    string // "xxxxxxxx-0000-0000-0000-": the run-unique id prefix
	tenantIDs []string
	startedAt string // database clock before the sweeps ran
}

func (f retentionReviewsFixture) id(suffix string) string { return f.prefix + suffix }

func (f retentionReviewsFixture) tenantList() string {
	quoted := make([]string, len(f.tenantIDs))
	for i, id := range f.tenantIDs {
		quoted[i] = "'" + id + "'"
	}
	return strings.Join(quoted, ", ")
}

// NULL renders empty, as psql -tA would.
func retentionReviewsCount(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var value sql.NullString
	mustNoErr(t, db.QueryRow(query, args...).Scan(&value), "query "+query)
	if !value.Valid {
		return ""
	}
	return value.String
}

func retentionReviewsExpect(t *testing.T, db *sql.DB, want, why, query string, args ...any) {
	t.Helper()
	if got := retentionReviewsCount(t, db, query, args...); got != want {
		t.Fatalf("%s: got %q, want %q (query: %s)", why, got, want, query)
	}
}

func retentionReviewsSeed(t *testing.T) retentionReviewsFixture {
	t.Helper()
	url := testpg.URL(t)
	db, err := sql.Open("pgx", url)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	raw := make([]byte, 4)
	_, err = rand.Read(raw)
	mustNoErr(t, err, "random id prefix")
	tag := hex.EncodeToString(raw)
	f := retentionReviewsFixture{db: db, prefix: tag + "-0000-0000-0000-"}
	for _, n := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "a", "b"} {
		f.tenantIDs = append(f.tenantIDs, f.id("00000000000"+n))
	}
	t.Cleanup(func() { retentionReviewsCleanup(t, f) })

	fixture := strings.NewReplacer("{{p}}", f.prefix, "{{n}}", "-"+tag).Replace(retentionReviewsFixtureSQL)
	_, err = db.ExecContext(context.Background(), fixture)
	mustNoErr(t, err, "seed fixtures")
	f.psql = startRetentionPsql(t, context.Background(), url, retentionReviewsPolicy)
	f.startedAt = retentionReviewsCount(t, db, `SELECT clock_timestamp()::text`)
	return f
}

func retentionReviewsCleanup(t *testing.T, f retentionReviewsFixture) {
	t.Helper()
	in := "(" + f.tenantList() + ")"
	for _, stmt := range []string{
		`DELETE FROM review_merge_queue WHERE tenant_id IN ` + in,
		`DELETE FROM releases WHERE tenant_id IN ` + in,
		`DELETE FROM comments WHERE tenant_id IN ` + in,
		`DELETE FROM review_reviewers WHERE tenant_id IN ` + in,
		`UPDATE reviews SET last_failed_build_id = NULL, last_ready_build_id = NULL, last_merged_build_id = NULL, status = 'CLOSED' WHERE tenant_id IN ` + in,
		`DELETE FROM builds WHERE tenant_id IN ` + in,
		`DELETE FROM reviews WHERE tenant_id IN ` + in,
		`DELETE FROM users WHERE tenant_id IN ` + in,
		`DELETE FROM tenants WHERE tenant_id IN ` + in,
	} {
		if _, err := f.db.Exec(stmt); err != nil {
			t.Logf("retention reviews cleanup (%s): %v", stmt, err)
		}
	}
	if f.startedAt != "" {
		if _, err := f.db.Exec(`DELETE FROM retention_runs WHERE policy_name = 'reviews' AND created_at >= $1::timestamptz`, f.startedAt); err != nil {
			t.Logf("retention reviews cleanup (retention_runs): %v", err)
		}
	}
}

func retentionReviewsReportHas(out, table, count string) bool {
	return retentionReport(out)[table] == count
}

func retentionReviewsRunValue(t *testing.T, f retentionReviewsFixture, column, table string, dryRun bool) string {
	t.Helper()
	return retentionReviewsCount(t, f.db,
		`SELECT `+column+` FROM retention_runs WHERE policy_name = 'reviews' AND table_name = $1 AND dry_run = $2 AND created_at >= $3::timestamptz ORDER BY created_at DESC LIMIT 1`,
		table, dryRun, f.startedAt)
}

func TestRetentionReviewsSweepDeletesOnlyUnreferencedAgedClosedReviews(t *testing.T) {
	f := retentionReviewsSeed(t)
	db := f.db
	scope := "tenant_id IN (" + f.tenantList() + ")"

	initialReviews := retentionReviewsCount(t, db, `SELECT count(*) FROM reviews WHERE `+scope)
	initialReviewers := retentionReviewsCount(t, db, `SELECT count(*) FROM review_reviewers WHERE `+scope)
	if initialReviews != "2015" {
		t.Fatalf("expected 2015 fixture reviews (10 + 2005 count-cap), got %s", initialReviews)
	}
	if initialReviewers != "3" {
		t.Fatalf("expected 3 fixture review_reviewers (clean, builds-unpinned, builds-pinned), got %s", initialReviewers)
	}

	report := f.psql.sweep(t, context.Background(), true)
	if !retentionReviewsReportHas(report, "reviews", "6") {
		t.Fatalf("dry run must report 6 reviews eligible (1 clean + 5 count-cap), got:\n%s", report)
	}
	if !retentionReviewsReportHas(report, "review_reviewers", "1") {
		t.Fatalf("dry run must report 1 review_reviewers eligible (the clean review's), got:\n%s", report)
	}
	retentionReviewsExpect(t, db, initialReviews, "dry run must not delete any reviews", `SELECT count(*) FROM reviews WHERE `+scope)
	retentionReviewsExpect(t, db, initialReviewers, "dry run must not delete any review_reviewers", `SELECT count(*) FROM review_reviewers WHERE `+scope)
	retentionReviewsExpect(t, db, "false", "dry run must not null the stale pin either",
		`SELECT last_failed_build_id IS NULL FROM reviews WHERE review_id = $1`, f.id("000000000301"))

	for _, c := range []struct{ column, table, want, why string }{
		{"eligible_count", "reviews", "6", "the dry run must record 6 reviews eligible in retention_runs"},
		{"deleted_count", "reviews", "0", "the dry run must record 0 reviews deleted in retention_runs"},
		{"eligible_count", "review_reviewers", "1", "the dry run must record 1 review_reviewers eligible in retention_runs"},
		{"deleted_count", "review_reviewers", "0", "the dry run must record 0 review_reviewers deleted in retention_runs"},
	} {
		if got := retentionReviewsRunValue(t, f, c.column, c.table, true); got != c.want {
			t.Fatalf("%s: got %q", c.why, got)
		}
	}

	f.psql.sweep(t, context.Background(), false)

	for _, c := range []struct{ column, table, want, why string }{
		{"eligible_count", "reviews", "6", "the real run must record 6 reviews eligible in retention_runs"},
		{"deleted_count", "reviews", "6", "the real run must record 6 reviews deleted in retention_runs"},
		{"eligible_count", "review_reviewers", "1", "the real run must record 1 review_reviewers eligible in retention_runs"},
		{"deleted_count", "review_reviewers", "1", "the real run must record 1 review_reviewers deleted in retention_runs"},
	} {
		if got := retentionReviewsRunValue(t, f, c.column, c.table, false); got != c.want {
			t.Fatalf("%s: got %q", c.why, got)
		}
	}

	reviewCount := `SELECT count(*) FROM reviews WHERE review_id = $1`
	reviewerCount := `SELECT count(*) FROM review_reviewers WHERE review_id = $1`
	buildCount := `SELECT count(*) FROM builds WHERE build_id = $1`

	retentionReviewsExpect(t, db, "0", "the clean CLOSED review must be deleted", reviewCount, f.id("000000000101"))
	retentionReviewsExpect(t, db, "0", "the clean review's reviewer must be deleted with it", reviewerCount, f.id("000000000101"))

	retentionReviewsExpect(t, db, "1", "a CLOSED review with an unpinned builds row must survive", reviewCount, f.id("000000000201"))
	retentionReviewsExpect(t, db, "1", "its reviewer must survive too", reviewerCount, f.id("000000000201"))
	retentionReviewsExpect(t, db, "1", "the unpinned build itself must be untouched", buildCount, f.id("000000000202"))

	// The stale pin is nulled even though the review survives.
	retentionReviewsExpect(t, db, "1", "a CLOSED review with a pinned builds row must survive", reviewCount, f.id("000000000301"))
	retentionReviewsExpect(t, db, "1", "its reviewer must survive too", reviewerCount, f.id("000000000301"))
	retentionReviewsExpect(t, db, "", "the stale last_failed_build_id pin must be nulled",
		`SELECT last_failed_build_id::text FROM reviews WHERE review_id = $1`, f.id("000000000301"))
	retentionReviewsExpect(t, db, "1", "the pinned build itself must survive -- this policy never touches builds", buildCount, f.id("000000000302"))

	retentionReviewsExpect(t, db, "1", "a CLOSED review referenced by a comment must survive", reviewCount, f.id("000000000401"))
	retentionReviewsExpect(t, db, "1", "a CLOSED review referenced by a release must survive", reviewCount, f.id("000000000501"))
	retentionReviewsExpect(t, db, "1", "a CLOSED review referenced by a historical GATE build must survive", reviewCount, f.id("000000000601"))
	retentionReviewsExpect(t, db, "1", "the historical GATE build itself must be untouched", buildCount, f.id("000000000602"))
	retentionReviewsExpect(t, db, "1", "a CLOSED review referenced by a review_merge_queue row must survive", reviewCount, f.id("000000000701"))

	retentionReviewsExpect(t, db, "1", "an OPEN review must survive no matter how old it is", reviewCount, f.id("000000000801"))

	retentionReviewsExpect(t, db, "1", "a MERGED review must survive no matter how old it is", reviewCount, f.id("000000000901"))
	retentionReviewsExpect(t, db, "1", "a MERGED review's pinned build must survive", buildCount, f.id("000000000902"))

	retentionReviewsExpect(t, db, "1", "a CLOSED review within the age bound must survive", reviewCount, f.id("000000000a01"))

	capTenant := f.id("00000000000b")
	retentionReviewsExpect(t, db, "2000", "the count-cap tenant must keep exactly 2000 reviews",
		`SELECT count(*) FROM reviews WHERE tenant_id = $1`, capTenant)
	retentionReviewsExpect(t, db, "0", "the count-cap tenant's 5 oldest reviews must be deleted",
		`SELECT count(*) FROM reviews WHERE tenant_id = $1 AND name IN ('r-2001','r-2002','r-2003','r-2004','r-2005')`, capTenant)
	retentionReviewsExpect(t, db, "1", "the count-cap tenant's newest review must survive",
		`SELECT count(*) FROM reviews WHERE tenant_id = $1 AND name = 'r-1'`, capTenant)
}
