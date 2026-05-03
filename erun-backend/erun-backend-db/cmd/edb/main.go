package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	erunbackenddb "github.com/sophium/erun/erun-backend/erun-backend-db"

	_ "github.com/jackc/pgx/v5/stdlib"
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
	flags.StringVar(&cfg.DatabaseURL, "database-url", cfg.DatabaseURL, "Backend PostgreSQL database URL")
	flags.StringVar(&cfg.DatabaseDialect, "database-dialect", cfg.DatabaseDialect, "Backend database dialect; only postgres is supported")
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
		DatabaseURL:     strings.TrimSpace(os.Getenv("ERUN_DATABASE_URL")),
		DatabaseDialect: strings.TrimSpace(os.Getenv("ERUN_DATABASE_DIALECT")),
	}
}

func openDatabase(databaseURL string, configuredDialect string) (*sql.DB, string, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, "", fmt.Errorf("database URL is required")
	}
	dialect := strings.TrimSpace(configuredDialect)
	if dialect == "" {
		dialect = inferDialect(databaseURL)
	}
	dialect = normalizeDialect(dialect)
	if dialect != "postgres" {
		return nil, "", fmt.Errorf("unsupported database dialect %q", dialect)
	}

	dsn := strings.TrimSpace(databaseURL)
	db, err := sql.Open("pgx", dsn)
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
	case "":
		return ""
	case "postgres", "postgresql", "pgx":
		return "postgres"
	default:
		return strings.TrimSpace(strings.ToLower(dialect))
	}
}

func inferDialect(databaseURL string) string {
	value := strings.TrimSpace(strings.ToLower(databaseURL))
	if strings.HasPrefix(value, "postgres://") || strings.HasPrefix(value, "postgresql://") {
		return "postgres"
	}
	return ""
}
