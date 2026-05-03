package erunbackenddb

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"path"
	"sort"
	"strings"
)

//go:embed migrations/sqlite/*.sql migrations/postgres/*.sql
var migrationFiles embed.FS

type MigrationResult struct {
	Dialect string
	Applied []string
	Skipped []string
}

func (r MigrationResult) Summary() string {
	dialect := strings.TrimSpace(r.Dialect)
	if dialect == "" {
		dialect = "unknown"
	}
	if len(r.Applied) == 0 {
		return fmt.Sprintf("erun backend database migrations complete; dialect=%s applied=0 skipped=%d", dialect, len(r.Skipped))
	}
	return fmt.Sprintf("erun backend database migrations complete; dialect=%s applied=%d skipped=%d latest=%s", dialect, len(r.Applied), len(r.Skipped), r.Applied[len(r.Applied)-1])
}

func Migrate(ctx context.Context, db *sql.DB, dialect string) (MigrationResult, error) {
	dialect = normalizeDialect(dialect)
	result := MigrationResult{Dialect: dialect}
	if db == nil {
		return result, fmt.Errorf("database is required")
	}
	migrations, err := migrationNames(dialect)
	if err != nil {
		return result, err
	}
	if len(migrations) == 0 {
		return result, nil
	}
	if err := ensureMigrationTable(ctx, db); err != nil {
		return result, err
	}
	for _, name := range migrations {
		applied, err := migrationApplied(ctx, db, dialect, name)
		if err != nil {
			return result, err
		}
		if applied {
			result.Skipped = append(result.Skipped, name)
			continue
		}
		if err := applyMigration(ctx, db, dialect, name); err != nil {
			return result, err
		}
		result.Applied = append(result.Applied, name)
	}
	return result, nil
}

func normalizeDialect(dialect string) string {
	switch strings.TrimSpace(strings.ToLower(dialect)) {
	case "", "sqlite", "sqlite3":
		return "sqlite"
	case "postgres", "postgresql", "pgx":
		return "postgres"
	default:
		return strings.TrimSpace(strings.ToLower(dialect))
	}
}

func migrationNames(dialect string) ([]string, error) {
	dir := path.Join("migrations", dialect)
	entries, err := migrationFiles.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s migrations: %w", dialect, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func ensureMigrationTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS erun_schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("ensure schema migration table: %w", err)
	}
	return nil
}

func migrationApplied(ctx context.Context, db *sql.DB, dialect, name string) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, rebind(dialect, `SELECT COUNT(*) FROM erun_schema_migrations WHERE version = ?`), name).Scan(&count); err != nil {
		return false, fmt.Errorf("check migration %s: %w", name, err)
	}
	return count > 0, nil
}

func applyMigration(ctx context.Context, db *sql.DB, dialect, name string) error {
	filename := path.Join("migrations", dialect, name)
	sqlBytes, err := migrationFiles.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", name, err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, string(sqlBytes)); err != nil {
		return fmt.Errorf("apply migration %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, rebind(dialect, `INSERT INTO erun_schema_migrations (version) VALUES (?)`), name); err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", name, err)
	}
	return nil
}

func rebind(dialect, query string) string {
	if dialect != "postgres" {
		return query
	}
	return strings.ReplaceAll(query, "?", "$1")
}
