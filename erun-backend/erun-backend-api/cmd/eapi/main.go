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

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	backendapi "github.com/sophium/erun/erun-backend/erun-backend-api"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/mcptoken"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/secrets"

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
	flags.StringVar(&cfg.AllowedIssuers, "oidc-allowed-issuers", cfg.AllowedIssuers, "Comma-separated OIDC issuer allow-list; empty allows any issuer resolved from a token")
	flags.StringVar(&cfg.DesktopPublicKeyPath, "desktop-public-key-path", cfg.DesktopPublicKeyPath, "Path to the desktop Ed25519 public key; when set, the API trusts file://<path> desktop-signed tokens (issue #674), the same auth the MCP edge uses")
	flags.StringVar(&cfg.MCPSigningKeyPath, "mcp-signing-key-path", cfg.MCPSigningKeyPath, "Path to the backend's Ed25519 MCP signing private key; when set, the mcp-token endpoint mints per-env MCP bearer tokens for the console (unset disables it: the endpoint reports 501)")
	if err := flags.Parse(args); err != nil {
		return err
	}

	db, err := openDatabase(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	cipher, err := optionalCipher(cfg.SecretsKey)
	if err != nil {
		return err
	}
	dbosCtx, err := optionalDBOS(cfg.DBOSDatabaseURL)
	if err != nil {
		return err
	}
	mcpSigner, err := optionalMCPSigner(cfg.MCPSigningKeyPath)
	if err != nil {
		return err
	}

	handler, err := backendapi.NewHandler(backendapi.HandlerOptions{
		TokenVerifier: backendapi.NewBearerTokenVerifier(backendapi.BearerTokenVerifierOptions{
			AllowedIssuers:       splitCSV(cfg.AllowedIssuers),
			DesktopPublicKeyPath: cfg.DesktopPublicKeyPath,
		}),
		IdentityCache: backendapi.NewIdentityResolutionCache(backendapi.IdentityCacheOptions{}),
		DB:            db,
		DBDialect:     repository.DialectPostgres,
		DBOSContext:   dbosCtx,
		Cipher:        cipher,
		AWSEndpoint:   cfg.AWSEndpoint,
		MCPSigner:     mcpSigner,
	})
	if err != nil {
		return err
	}

	// Launch must run after NewHandler, which registers the provisioning workflow.
	if dbosCtx != nil {
		if err := dbos.Launch(dbosCtx); err != nil {
			return err
		}
		defer func() { dbos.Shutdown(dbosCtx, 5*time.Second) }()
		log.Print("erun api live provisioning enabled (DBOS durable workflows)")
	}

	server := http.Server{
		Addr:              net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port)),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("erun api listening on %s; database=postgres audit=postgres oidc allowed issuers=%d", server.Addr, len(splitCSV(cfg.AllowedIssuers)))
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
	Host                 string
	Port                 int
	DatabaseURL          string
	AllowedIssuers       string
	DesktopPublicKeyPath string
	MCPSigningKeyPath    string
	// These enable optional live context provisioning; it stays disabled unless
	// SecretsKey and DBOSDatabaseURL are both set. DBOSDatabaseURL is a separate
	// database from ERUN_DATABASE_URL. AWSEndpoint targets a local emulator for
	// verification (empty = real AWS).
	SecretsKey      string
	DBOSDatabaseURL string
	AWSEndpoint     string
}

func configFromEnv() apiConfig {
	return apiConfig{
		Host:                 envOrDefault("ERUN_API_HOST", "127.0.0.1"),
		Port:                 intEnvOrDefault("ERUN_API_PORT", 17033),
		DatabaseURL:          strings.TrimSpace(os.Getenv("ERUN_DATABASE_URL")),
		AllowedIssuers:       strings.TrimSpace(os.Getenv("ERUN_OIDC_ALLOWED_ISSUERS")),
		DesktopPublicKeyPath: strings.TrimSpace(os.Getenv("ERUN_API_DESKTOP_PUBLIC_KEY_PATH")),
		MCPSigningKeyPath:    strings.TrimSpace(os.Getenv("ERUN_API_MCP_SIGNING_KEY_PATH")),
		SecretsKey:           strings.TrimSpace(os.Getenv("ERUN_SECRETS_KEY")),
		DBOSDatabaseURL:      strings.TrimSpace(os.Getenv("DBOS_SYSTEM_DATABASE_URL")),
		AWSEndpoint:          strings.TrimSpace(os.Getenv("ERUN_AWS_ENDPOINT_URL")),
	}
}

func optionalCipher(key string) (*secrets.Cipher, error) {
	if key == "" {
		return nil, nil
	}
	return secrets.NewCipher(key)
}

// optionalMCPSigner loads the backend's MCP signing key when a path is set. A
// blank path disables per-env MCP token minting (the endpoint reports 501); a
// set-but-unreadable or invalid key is a hard misconfiguration, not a silent
// disable.
func optionalMCPSigner(path string) (*mcptoken.Signer, error) {
	if path == "" {
		return nil, nil
	}
	privatePEM, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read mcp signing key %s: %w", path, err)
	}
	return mcptoken.NewSigner(privatePEM)
}

func optionalDBOS(databaseURL string) (dbos.DBOSContext, error) {
	if databaseURL == "" {
		return nil, nil
	}
	return dbos.NewDBOSContext(context.Background(), dbos.Config{
		AppName:     "erun-api",
		DatabaseURL: databaseURL,
	})
}

func openDatabase(databaseURL string) (*sql.DB, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, fmt.Errorf("database URL is required")
	}
	if inferDatabase(databaseURL) != repository.DialectPostgres {
		return nil, fmt.Errorf("database URL must be PostgreSQL")
	}

	dsn := strings.TrimSpace(databaseURL)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func inferDatabase(databaseURL string) repository.Dialect {
	value := strings.TrimSpace(strings.ToLower(databaseURL))
	if strings.HasPrefix(value, "postgres://") || strings.HasPrefix(value, "postgresql://") {
		return repository.DialectPostgres
	}
	return ""
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
