package erunbackenddb

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrateAppliesSQLiteMigrations(t *testing.T) {
	db := openMigrationTestDB(t)
	result, err := Migrate(context.Background(), db, "sqlite")
	if err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	if len(result.Applied) != 1 || len(result.Skipped) != 0 {
		t.Fatalf("unexpected migration result: %+v", result)
	}
	if !sqliteTableExists(t, db, "tenants") || !sqliteTableExists(t, db, "reviews") || !sqliteTableExists(t, db, "audit_events") {
		t.Fatalf("expected schema tables to be created")
	}
}

func TestMigrateSkipsAppliedSQLiteMigrations(t *testing.T) {
	db := openMigrationTestDB(t)
	if _, err := Migrate(context.Background(), db, "sqlite"); err != nil {
		t.Fatalf("initial Migrate failed: %v", err)
	}
	result, err := Migrate(context.Background(), db, "sqlite")
	if err != nil {
		t.Fatalf("second Migrate failed: %v", err)
	}
	if len(result.Applied) != 0 || len(result.Skipped) != 1 {
		t.Fatalf("unexpected migration result: %+v", result)
	}
}

func openMigrationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func sqliteTableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
		t.Fatalf("query sqlite schema failed: %v", err)
	}
	return count > 0
}
