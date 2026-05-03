package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	backendapi "github.com/sophium/erun/erun-backend/erun-backend-api"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"

	_ "github.com/jackc/pgx/v5/stdlib"
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
	flags.StringVar(&cfg.DatabaseURL, "database-url", cfg.DatabaseURL, "Backend PostgreSQL database URL")
	flags.StringVar(&cfg.DatabaseDialect, "database-dialect", cfg.DatabaseDialect, "Backend database dialect; only postgres is supported")
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
	log.Printf("erun api listening on %s; database dialect=%s; oidc allowed issuers=%d", server.Addr, dialect, len(splitCSV(cfg.AllowedIssuers)))
	log.Print(identityBootstrapStatus(context.Background(), db))
	return server.ListenAndServe()
}

func identityBootstrapStatus(ctx context.Context, db *sql.DB) string {
	tenants, tenantErr := countRows(ctx, db, "tenants")
	users, userErr := countRows(ctx, db, "users")
	issuers, issuerErr := countRows(ctx, db, "tenant_issuers")
	if tenantErr != nil || userErr != nil || issuerErr != nil {
		return fmt.Sprintf("erun api identity status unavailable; tenants=%s users=%s issuers=%s", countStatus(tenants, tenantErr), countStatus(users, userErr), countStatus(issuers, issuerErr))
	}
	if tenants == 0 {
		return "erun api identity bootstrap pending; firstTenant=false firstUser=false tenants=0 users=0 issuers=0"
	}
	if users == 0 {
		return fmt.Sprintf("erun api identity bootstrap pending; firstTenant=true firstUser=false tenants=%d users=0 issuers=%d", tenants, issuers)
	}
	return fmt.Sprintf("erun api identity ready; firstTenant=true firstUser=true tenants=%d users=%d issuers=%d", tenants, users, issuers)
}

func countRows(ctx context.Context, db *sql.DB, table string) (int, error) {
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func countStatus(count int, err error) string {
	if err != nil {
		return "error(" + err.Error() + ")"
	}
	return fmt.Sprintf("%d", count)
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
		DatabaseURL:     strings.TrimSpace(os.Getenv("ERUN_DATABASE_URL")),
		DatabaseDialect: strings.TrimSpace(os.Getenv("ERUN_DATABASE_DIALECT")),
		AllowedIssuers:  strings.TrimSpace(os.Getenv("ERUN_OIDC_ALLOWED_ISSUERS")),
	}
}

func openDatabase(databaseURL string, configuredDialect string) (*sql.DB, repository.Dialect, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, "", fmt.Errorf("database URL is required")
	}
	dialect := normalizeDialect(configuredDialect)
	if dialect == "" {
		dialect = inferDialect(databaseURL)
	}
	if dialect != repository.DialectPostgres {
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

func inferDialect(databaseURL string) repository.Dialect {
	value := strings.TrimSpace(strings.ToLower(databaseURL))
	if strings.HasPrefix(value, "postgres://") || strings.HasPrefix(value, "postgresql://") {
		return repository.DialectPostgres
	}
	return ""
}

func normalizeDialect(dialect string) repository.Dialect {
	switch strings.TrimSpace(strings.ToLower(dialect)) {
	case "":
		return ""
	case "postgres", "postgresql", "pgx":
		return repository.DialectPostgres
	default:
		return repository.Dialect(strings.TrimSpace(strings.ToLower(dialect)))
	}
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
