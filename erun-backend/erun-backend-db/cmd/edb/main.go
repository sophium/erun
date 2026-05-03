package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	erunbackenddb "github.com/sophium/erun/erun-backend/erun-backend-db"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	command := "migrate"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = strings.TrimSpace(args[0])
		args = args[1:]
	}
	if command != "migrate" {
		return fmt.Errorf("unsupported command %q", command)
	}

	cfg := configFromEnv()
	flags := flag.NewFlagSet("edb migrate", flag.ContinueOnError)
	flags.StringVar(&cfg.DatabaseURL, "database-url", cfg.DatabaseURL, "Backend database URL; supports sqlite and postgres")
	flags.StringVar(&cfg.DatabaseDialect, "database-dialect", cfg.DatabaseDialect, "Backend database dialect: sqlite or postgres")
	if err := flags.Parse(args); err != nil {
		return err
	}

	db, dialect, err := openDatabase(cfg.DatabaseURL, cfg.DatabaseDialect)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := erunbackenddb.Migrate(ctx, db, dialect)
	if err != nil {
		return fmt.Errorf("run database migrations: %w", err)
	}
	log.Print(result.Summary())
	return nil
}

type dbConfig struct {
	DatabaseURL     string
	DatabaseDialect string
}

func configFromEnv() dbConfig {
	return dbConfig{
		DatabaseURL:     envOrDefault("ERUN_DATABASE_URL", defaultSQLiteURL()),
		DatabaseDialect: strings.TrimSpace(os.Getenv("ERUN_DATABASE_DIALECT")),
	}
}

func openDatabase(databaseURL string, configuredDialect string) (*sql.DB, string, error) {
	dialect := strings.TrimSpace(configuredDialect)
	if dialect == "" {
		dialect = inferDialect(databaseURL)
	}
	dialect = normalizeDialect(dialect)

	driver := ""
	dsn := strings.TrimSpace(databaseURL)
	switch dialect {
	case "sqlite":
		driver = "sqlite"
		dsn = sqliteDSN(dsn)
		if err := ensureSQLiteDirectory(dsn); err != nil {
			return nil, "", err
		}
	case "postgres":
		driver = "pgx"
	case "":
		return nil, "", fmt.Errorf("database dialect is required")
	default:
		return nil, "", fmt.Errorf("unsupported database dialect %q", dialect)
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, "", err
	}
	return db, dialect, nil
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

func ensureSQLiteDirectory(dsn string) error {
	value := strings.TrimPrefix(strings.TrimSpace(dsn), "file:")
	if idx := strings.Index(value, "?"); idx >= 0 {
		value = value[:idx]
	}
	if value == "" || value == ":memory:" {
		return nil
	}
	return os.MkdirAll(filepath.Dir(filepath.Clean(value)), 0o755)
}

func inferDialect(databaseURL string) string {
	value := strings.TrimSpace(strings.ToLower(databaseURL))
	if strings.HasPrefix(value, "postgres://") || strings.HasPrefix(value, "postgresql://") {
		return "postgres"
	}
	return "sqlite"
}

func sqliteDSN(databaseURL string) string {
	value := strings.TrimSpace(databaseURL)
	if value == "" {
		value = defaultSQLiteURL()
	}
	if strings.HasPrefix(strings.ToLower(value), "sqlite://") {
		value = strings.TrimPrefix(value, "sqlite://")
	}
	if strings.HasPrefix(value, "file:") {
		return ensureSQLiteForeignKeys(value)
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme == "" && parsed.RawQuery != "" {
		value = parsed.Path + "?" + parsed.RawQuery
	}
	return ensureSQLiteForeignKeys("file:" + value)
}

func ensureSQLiteForeignKeys(dsn string) string {
	if strings.Contains(dsn, "_pragma=foreign_keys") {
		return dsn
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + "_pragma=foreign_keys(1)"
}

func defaultSQLiteURL() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "file:erun-backend.db"
	}
	return filepath.Join(home, ".erun", "erun-backend.db")
}

func envOrDefault(name string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}
