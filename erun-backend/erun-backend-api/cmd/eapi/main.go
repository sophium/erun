package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	backendapi "github.com/sophium/erun/erun-backend/erun-backend-api"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	cfg := configFromEnv()
	flags := flag.NewFlagSet("eapi", flag.ContinueOnError)
	flags.StringVar(&cfg.Host, "host", cfg.Host, "Host interface to bind the backend API HTTP server to")
	flags.IntVar(&cfg.Port, "port", cfg.Port, "Port to bind the backend API HTTP server to")
	flags.StringVar(&cfg.DatabaseURL, "database-url", cfg.DatabaseURL, "Backend database URL; supports sqlite and postgres")
	flags.StringVar(&cfg.DatabaseDialect, "database-dialect", cfg.DatabaseDialect, "Backend database dialect: sqlite or postgres")
	flags.StringVar(&cfg.AllowedIssuers, "oidc-allowed-issuers", cfg.AllowedIssuers, "Comma-separated OIDC issuer allow-list; empty allows any issuer resolved from a token")
	if err := flags.Parse(args); err != nil {
		return err
	}

	db, dialect, err := openDatabase(cfg.DatabaseURL, cfg.DatabaseDialect)
	if err != nil {
		return err
	}
	defer db.Close()

	handler, err := backendapi.NewHandler(backendapi.HandlerOptions{
		TokenVerifier: backendapi.NewOIDCTokenVerifier(splitCSV(cfg.AllowedIssuers)),
		DB:            db,
		DBDialect:     dialect,
	})
	if err != nil {
		return err
	}

	server := http.Server{
		Addr:              net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port)),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return server.ListenAndServe()
}

type apiConfig struct {
	Host            string
	Port            int
	DatabaseURL     string
	DatabaseDialect string
	AllowedIssuers  string
}

func configFromEnv() apiConfig {
	return apiConfig{
		Host:            envOrDefault("ERUN_API_HOST", "127.0.0.1"),
		Port:            intEnvOrDefault("ERUN_API_PORT", 17033),
		DatabaseURL:     envOrDefault("ERUN_DATABASE_URL", defaultSQLiteURL()),
		DatabaseDialect: strings.TrimSpace(os.Getenv("ERUN_DATABASE_DIALECT")),
		AllowedIssuers:  strings.TrimSpace(os.Getenv("ERUN_OIDC_ALLOWED_ISSUERS")),
	}
}

func openDatabase(databaseURL string, configuredDialect string) (*sql.DB, repository.Dialect, error) {
	dialect := repository.Dialect(strings.TrimSpace(configuredDialect))
	if dialect == "" {
		dialect = inferDialect(databaseURL)
	}

	driver := ""
	dsn := strings.TrimSpace(databaseURL)
	switch dialect {
	case repository.DialectSQLite:
		driver = "sqlite"
		dsn = sqliteDSN(dsn)
		if err := ensureSQLiteDirectory(dsn); err != nil {
			return nil, "", err
		}
	case repository.DialectPostgres:
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

func inferDialect(databaseURL string) repository.Dialect {
	value := strings.TrimSpace(strings.ToLower(databaseURL))
	if strings.HasPrefix(value, "postgres://") || strings.HasPrefix(value, "postgresql://") {
		return repository.DialectPostgres
	}
	return repository.DialectSQLite
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

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func envOrDefault(name string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func intEnvOrDefault(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
