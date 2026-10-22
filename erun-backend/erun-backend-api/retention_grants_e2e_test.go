package backendapi

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/testpg"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// End-to-end proof that erun_operations -- the role every retention policy in
// erun-backend-db/retention/ runs as, via `SET ROLE erun_operations;` -- can
// actually DELETE from every table a retention policy sweeps, against the
// real migrated PostgreSQL testpg serves rather than a catalog-privilege
// query. The ai_sessions retention design shipped with a gap it named but
// did not close: the role had SELECT/INSERT/UPDATE/REFERENCES on ai_sessions
// but no DELETE, so that policy could never delete a row. This locks that the
// gap is closed, and that it was the only one: every other table under an
// implemented or designed retention policy (the six-table sweep --
// comments, releases, reviews, ai_sessions, invites, invite_requests -- plus
// builds/gate_runs, which predate the sweep) grants DELETE too.
//
// A has_table_privilege() check can be satisfied by a GRANT that looks right
// on paper while row-level security still blocks the statement, so every
// assertion runs a real INSERT then DELETE inside a transaction that rolls
// back. audit_events and usage_events are asserted the other way: they
// withhold DELETE pending a compliance decision, so a
// delete that succeeds there is a silent privilege escalation.
//
// Ported from erun-devops/docker/erun-backend-db/retention_grants_test.sh.

const (
	retentionGrantsTenantID      = "00000000-0000-0000-0000-000000000001"
	retentionGrantsUserID        = "00000000-0000-0000-0000-000000000002"
	retentionGrantsEnvironmentID = "00000000-0000-0000-0000-000000000003"
	retentionGrantsReviewID      = "00000000-0000-0000-0000-000000000004"
)

// retentionGrantsDatabase opens the package's migrated database (as the
// superuser testpg's URL names) and seeds the shared fixtures every per-table
// assertion FKs against, removing them again when the test ends.
func retentionGrantsDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", testpg.URL(t))
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM reviews WHERE review_id = '` + retentionGrantsReviewID + `'`,
			`DELETE FROM environments WHERE environment_id = '` + retentionGrantsEnvironmentID + `'`,
			`DELETE FROM users WHERE user_id = '` + retentionGrantsUserID + `'`,
			`DELETE FROM tenants WHERE tenant_id = '` + retentionGrantsTenantID + `'`,
		} {
			if _, err := db.Exec(stmt); err != nil {
				t.Logf("retention grants cleanup %q: %v", stmt, err)
			}
		}
	})
	for _, stmt := range []string{
		`INSERT INTO tenants (tenant_id, name) VALUES
		  ('00000000-0000-0000-0000-000000000001', 'grants-test-tenant')`,
		`INSERT INTO users (user_id, tenant_id, username) VALUES
		  ('00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'grants-test-user')`,
		`INSERT INTO environments (tenant_id, environment_id, name, type) VALUES
		  ('00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000003', 'grants-test-env', 'local-agent')`,
		`INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status) VALUES
		  ('00000000-0000-0000-0000-000000000004', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000002', 'grants-test-review', 'main', 'feat-grants', 'OPEN')`,
	} {
		_, err := db.Exec(stmt)
		mustNoErr(t, err, "seed retention grants fixture")
	}
	return db
}

// retentionGrantsAsOperations runs fn inside a transaction that has switched
// to erun_operations with SET LOCAL ROLE, and always rolls it back -- so no
// case depends on ordering or leaves state for the next.
func retentionGrantsAsOperations(t *testing.T, db *sql.DB, fn func(tx *sql.Tx) error) error {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	mustNoErr(t, err, "begin")
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET LOCAL ROLE erun_operations`); err != nil {
		t.Fatalf("SET LOCAL ROLE erun_operations: %v", err)
	}
	return fn(tx)
}

type retentionGrantsCase struct {
	name   string
	insert string
	delete string
}

func TestRetentionGrantsOperationsCanDeleteEverySweptTable(t *testing.T) {
	db := retentionGrantsDatabase(t)

	cases := []retentionGrantsCase{
		// The six-table sweep.
		{
			name: "comments",
			insert: `INSERT INTO comments (tenant_id, review_id, creator_user_id, status, commit_id, file_path, line, body)
VALUES ('00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000004', '00000000-0000-0000-0000-000000000002', 'OPEN', 'c1', 'f.go', 1, 'grants test comment')`,
			delete: `DELETE FROM comments WHERE tenant_id = '00000000-0000-0000-0000-000000000001' AND file_path = 'f.go'`,
		},
		{
			name: "releases",
			insert: `INSERT INTO releases (tenant_id, target_branch, commit_id, status)
VALUES ('00000000-0000-0000-0000-000000000001', 'main', 'rel-commit-1', 'queued')`,
			delete: `DELETE FROM releases WHERE tenant_id = '00000000-0000-0000-0000-000000000001' AND commit_id = 'rel-commit-1'`,
		},
		{
			name: "reviews",
			insert: `INSERT INTO reviews (review_id, tenant_id, author_user_id, name, target_branch, source_branch, status)
VALUES ('00000000-0000-0000-0000-000000000005', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000002', 'grants-test-review-disposable', 'main', 'feat-grants-disposable', 'OPEN')`,
			delete: `DELETE FROM reviews WHERE review_id = '00000000-0000-0000-0000-000000000005'`,
		},
		{
			name: "ai_sessions",
			insert: `INSERT INTO ai_sessions (tenant_id, environment_id, session_id, event, occurred_at)
VALUES ('00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000003', 'grants-test-session', 'exit', now())`,
			delete: `DELETE FROM ai_sessions WHERE tenant_id = '00000000-0000-0000-0000-000000000001' AND session_id = 'grants-test-session'`,
		},
		{
			name: "invites",
			insert: `INSERT INTO invites (invite_id, tenant_id, created_by_user_id, issuer, token, expires_at)
VALUES ('00000000-0000-0000-0000-000000000006', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000002', 'https://issuer.example', 'grants-test-token', now() + interval '7 days')`,
			delete: `DELETE FROM invites WHERE invite_id = '00000000-0000-0000-0000-000000000006'`,
		},
		{
			name: "invite_requests",
			insert: `INSERT INTO invite_requests (invite_request_id, issuer, subject, kind, tenant_name, status)
VALUES ('00000000-0000-0000-0000-000000000007', 'https://issuer.example', 'grants-test-subject', 'JOIN_TENANT', 'grants-test-tenant', 'PENDING')`,
			delete: `DELETE FROM invite_requests WHERE invite_request_id = '00000000-0000-0000-0000-000000000007'`,
		},
		// Predating the sweep.
		{
			name: "builds",
			insert: `INSERT INTO builds (build_id, tenant_id, environment_id, kind, successful, commit_id, version)
VALUES ('00000000-0000-0000-0000-000000000008', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000003', 'RECORDED', true, 'build-commit-1', 'v0.0.1')`,
			delete: `DELETE FROM builds WHERE build_id = '00000000-0000-0000-0000-000000000008'`,
		},
		{
			name: "gate_runs",
			insert: `INSERT INTO gate_runs (gate_run_id, tenant_id, source_branch, target_branch, source_commit, merge_commit, status)
VALUES ('00000000-0000-0000-0000-000000000009', '00000000-0000-0000-0000-000000000001', 'feat-grants', 'main', 'src-commit-1', 'merge-commit-1', 'RUNNING')`,
			delete: `DELETE FROM gate_runs WHERE gate_run_id = '00000000-0000-0000-0000-000000000009'`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var deleted int64
			err := retentionGrantsAsOperations(t, db, func(tx *sql.Tx) error {
				if _, err := tx.Exec(tc.insert); err != nil {
					return err
				}
				res, err := tx.Exec(tc.delete)
				if err != nil {
					return err
				}
				deleted, err = res.RowsAffected()
				return err
			})
			if err != nil {
				t.Fatalf("erun_operations must be able to INSERT then DELETE a %s row (retention policy would fail at runtime otherwise): %v", tc.name, err)
			}
			// Stronger than the shell script, which only checked psql's exit
			// status: a DELETE that row-level security silently filters to
			// zero rows also leaves a retention policy deleting nothing.
			if deleted != 1 {
				t.Fatalf("erun_operations DELETE on %s removed %d rows, want 1 (row-level security filtering the delete?)", tc.name, deleted)
			}
		})
	}
}

func TestRetentionGrantsAppendOnlyTablesRefuseOperationsDelete(t *testing.T) {
	db := retentionGrantsDatabase(t)

	// Predating the sweep, deliberately append-only.
	cases := []retentionGrantsCase{
		{
			name: "audit_events",
			insert: `INSERT INTO audit_events (tenant_id, erun_user_id, external_user_id, external_issuer_id, type, api_method, api_path)
VALUES ('00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000002', 'ext-user-1', 'https://issuer.example', 'API', 'GET', '/v1/grants-test')`,
			delete: `DELETE FROM audit_events WHERE tenant_id = '00000000-0000-0000-0000-000000000001'`,
		},
		{
			name: "usage_events",
			insert: `INSERT INTO usage_events (tenant_id, environment_id, event_type)
VALUES ('00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000003', 'environment_provisioned')`,
			delete: `DELETE FROM usage_events WHERE tenant_id = '00000000-0000-0000-0000-000000000001'`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The role keeps its normal write access...
			if err := retentionGrantsAsOperations(t, db, func(tx *sql.Tx) error {
				_, err := tx.Exec(tc.insert)
				return err
			}); err != nil {
				t.Fatalf("erun_operations must still be able to INSERT a %s row: %v", tc.name, err)
			}
			// ...but its delete is refused with a permission error.
			err := retentionGrantsAsOperations(t, db, func(tx *sql.Tx) error {
				_, err := tx.Exec(tc.delete)
				return err
			})
			if err == nil {
				t.Fatalf("erun_operations must NOT be able to DELETE from %s -- the append-only carve-out has regressed", tc.name)
			}
			if !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
				t.Fatalf("the %s delete must be refused with a permission error, got: %v", tc.name, err)
			}
		})
	}
}
