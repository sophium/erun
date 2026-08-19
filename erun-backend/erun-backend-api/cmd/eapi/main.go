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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	backendapi "github.com/sophium/erun/erun-backend/erun-backend-api"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/dns01broker"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/mcptoken"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/provision"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/routes"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/secrets"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	cfg, err := resolveConfig(args)
	if err != nil {
		return err
	}

	db, err := openDatabase(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	optional, err := optionalDependencies(cfg)
	if err != nil {
		return err
	}
	dbosCtx := optional.dbosCtx

	handler, err := backendapi.NewHandler(backendapi.HandlerOptions{
		TokenVerifier: backendapi.NewBearerTokenVerifier(backendapi.BearerTokenVerifierOptions{
			AllowedIssuers:       splitCSV(cfg.AllowedIssuers),
			DesktopPublicKeyPath: cfg.DesktopPublicKeyPath,
		}),
		IdentityCache: backendapi.NewIdentityResolutionCache(backendapi.IdentityCacheOptions{}),
		DB:            db,
		DBDialect:     repository.DialectPostgres,
		DBOSContext:   dbosCtx,
		Cipher:        optional.cipher,
		AWSEndpoint:   cfg.AWSEndpoint,
		MCPSigner:     optional.mcpSigner,
		DNS01Broker:   optional.dns01Broker,
		KubeClient:    optional.kubeClient,
		EnvDeploy: provision.EnvDeployConfig{
			Registry:               cfg.EnvDeployRegistry,
			PlatformNamespace:      cfg.PlatformNamespace,
			DeployerServiceAccount: cfg.EnvDeployerServiceAccount,
		},
		Release: provision.ReleaseConfig{
			Registry:       cfg.EnvDeployRegistry,
			RuntimeVersion: cfg.ReleaseRuntimeVersion,
			Namespace:      cfg.ReleaseNamespace,
			ServiceAccount: cfg.ReleaseServiceAccount,
			HomeClaim:      cfg.ReleaseHomeClaim,
			WorkspaceClaim: cfg.ReleaseWorkspaceClaim,
			RepoPath:       cfg.ReleaseRepoPath,
			DryRun:         cfg.ReleaseDryRun,
		},
		Platform: routes.PlatformInfo{
			Issuer:          cfg.PlatformIssuer,
			APIURL:          cfg.PlatformAPIURL,
			ConsoleURL:      cfg.PlatformConsoleURL,
			ConsoleClientID: cfg.PlatformConsoleClientID,
			CLIClientID:     cfg.PlatformCLIClientID,
			Brand:           cfg.PlatformBrand,
		},
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

// resolveConfig layers command-line flags over the environment-derived config.
func resolveConfig(args []string) (apiConfig, error) {
	cfg := configFromEnv()
	flags := flag.NewFlagSet("eapi", flag.ContinueOnError)
	flags.StringVar(&cfg.Host, "host", cfg.Host, "Host interface to bind the backend API HTTP server to")
	flags.IntVar(&cfg.Port, "port", cfg.Port, "Port to bind the backend API HTTP server to")
	flags.StringVar(&cfg.DatabaseURL, "database-url", cfg.DatabaseURL, "Backend PostgreSQL database URL")
	flags.StringVar(&cfg.AllowedIssuers, "oidc-allowed-issuers", cfg.AllowedIssuers, "Comma-separated OIDC issuer allow-list; empty allows any issuer resolved from a token")
	flags.StringVar(&cfg.DesktopPublicKeyPath, "desktop-public-key-path", cfg.DesktopPublicKeyPath, "Path to the desktop Ed25519 public key; when set, the API trusts file://<path> desktop-signed tokens (issue #674), the same auth the MCP edge uses")
	flags.StringVar(&cfg.MCPSigningKeyPath, "mcp-signing-key-path", cfg.MCPSigningKeyPath, "Path to the backend's Ed25519 MCP signing private key; when set, the mcp-token endpoint mints per-env MCP bearer tokens for the console (unset disables it: the endpoint reports 501)")
	flags.StringVar(&cfg.PlatformIssuer, "platform-issuer", cfg.PlatformIssuer, "This instance's OIDC issuer URL, served unauthenticated at GET /v1/platform")
	flags.StringVar(&cfg.PlatformAPIURL, "platform-api-url", cfg.PlatformAPIURL, "This instance's own API base URL, served unauthenticated at GET /v1/platform")
	flags.StringVar(&cfg.PlatformConsoleURL, "platform-console-url", cfg.PlatformConsoleURL, "This instance's hosted console URL, served unauthenticated at GET /v1/platform")
	flags.StringVar(&cfg.PlatformConsoleClientID, "platform-console-client-id", cfg.PlatformConsoleClientID, "The OIDC client id the hosted console authenticates with, served unauthenticated at GET /v1/platform")
	flags.StringVar(&cfg.PlatformCLIClientID, "platform-cli-client-id", cfg.PlatformCLIClientID, "The OIDC client id an erun CLI/agent authenticates with, served unauthenticated at GET /v1/platform")
	flags.StringVar(&cfg.PlatformBrand, "platform-brand", cfg.PlatformBrand, "This instance's display name, served unauthenticated at GET /v1/platform")
	if err := flags.Parse(args); err != nil {
		return apiConfig{}, err
	}
	return cfg, nil
}

// apiDependencies are the runtime pieces the API only gets when their
// configuration is present; each stays nil otherwise, disabling its feature.
type apiDependencies struct {
	cipher      *secrets.Cipher
	dbosCtx     dbos.DBOSContext
	mcpSigner   *mcptoken.Signer
	dns01Broker *dns01broker.Broker
	kubeClient  kubernetes.Interface
}

func optionalDependencies(cfg apiConfig) (apiDependencies, error) {
	cipher, err := optionalCipher(cfg.SecretsKey)
	if err != nil {
		return apiDependencies{}, err
	}
	dbosCtx, err := optionalDBOS(cfg.DBOSDatabaseURL)
	if err != nil {
		return apiDependencies{}, err
	}
	mcpSigner, err := optionalMCPSigner(cfg.MCPSigningKeyPath)
	if err != nil {
		return apiDependencies{}, err
	}
	dns01Broker, err := optionalDNS01Broker(cfg, mcpSigner)
	if err != nil {
		return apiDependencies{}, err
	}
	kubeClient, err := optionalKubeClient(cfg)
	if err != nil {
		return apiDependencies{}, err
	}
	return apiDependencies{
		cipher:      cipher,
		dbosCtx:     dbosCtx,
		mcpSigner:   mcpSigner,
		dns01Broker: dns01Broker,
		kubeClient:  kubeClient,
	}, nil
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
	// DNS-01 broker write path, injected at deploy from the platform env's
	// erun-powerdns TSIG Secret. Absent → the broker is disabled (its endpoints
	// are not registered).
	DNS01ServicesZone  string
	DNS01Nameserver    string
	DNS01TSIGKeyName   string
	DNS01TSIGAlgorithm string
	DNS01TSIGSecret    string
	// These enable optional live context provisioning; it stays disabled unless
	// SecretsKey and DBOSDatabaseURL are both set. DBOSDatabaseURL is a separate
	// database from ERUN_DATABASE_URL. AWSEndpoint targets a local emulator for
	// verification (empty = real AWS).
	SecretsKey      string
	DBOSDatabaseURL string
	AWSEndpoint     string
	// Server-side env-deploy executor (#605). EnvDeployerServiceAccount is the
	// cluster-admin SA the deploy Job runs as; setting it enables live env
	// provisioning (which then also needs an in-cluster kube client and
	// DBOSContext). PlatformNamespace is the namespace the Jobs run in (the
	// backend's own, via the downward API). EnvDeployRegistry is the image
	// registry the tenant's <tenant>-devops runtime image is pulled from.
	EnvDeployerServiceAccount string
	PlatformNamespace         string
	EnvDeployRegistry         string
	// Server-side release executor. The release runs as a Job in the agent
	// environment's own namespace so it lands beside that environment's warm
	// fingerprint cache and BuildKit state -- the thing an ephemeral runner cannot
	// offer. ReleaseRuntimeVersion tags the runtime image the release runs IN, not
	// the version it mints. The home/worktree claims and repo path are that
	// environment's own volumes; leaving them blank runs against the image-baked
	// project root instead. ReleaseDryRun resolves a release without publishing or
	// moving any public ref, which is how the executor is exercised against a
	// scoped target.
	ReleaseNamespace      string
	ReleaseServiceAccount string
	ReleaseRuntimeVersion string
	ReleaseHomeClaim      string
	ReleaseWorkspaceClaim string
	ReleaseRepoPath       string
	ReleaseDryRun         bool
	// Platform is this instance's own self-describing config, served
	// unauthenticated at GET /v1/platform so a client can discover it (issuer,
	// API/console URLs, OIDC client ids, brand) before it has a token. Every
	// field is optional; an absent value renders as an empty string, never an
	// error. ConsoleClientID/CLIClientID are typically sourced from the
	// erun-zitadel bootstrap's published ConfigMap, threaded in by the chart.
	PlatformIssuer          string
	PlatformAPIURL          string
	PlatformConsoleURL      string
	PlatformConsoleClientID string
	PlatformCLIClientID     string
	PlatformBrand           string
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
		DNS01ServicesZone:    strings.TrimSpace(os.Getenv("ERUN_DNS01_SERVICES_ZONE")),
		DNS01Nameserver:      strings.TrimSpace(os.Getenv("ERUN_DNS01_POWERDNS_NAMESERVER")),
		DNS01TSIGKeyName:     strings.TrimSpace(os.Getenv("ERUN_DNS01_TSIG_KEY_NAME")),
		DNS01TSIGAlgorithm:   strings.TrimSpace(os.Getenv("ERUN_DNS01_TSIG_ALGORITHM")),
		DNS01TSIGSecret:      strings.TrimSpace(os.Getenv("ERUN_DNS01_TSIG_SECRET")),

		EnvDeployerServiceAccount: strings.TrimSpace(os.Getenv("ERUN_ENV_DEPLOYER_SERVICE_ACCOUNT")),
		PlatformNamespace:         strings.TrimSpace(os.Getenv("POD_NAMESPACE")),
		EnvDeployRegistry:         envOrDefault("ERUN_ENV_DEPLOY_REGISTRY", "ghcr.io/sophium"),

		ReleaseNamespace:      strings.TrimSpace(os.Getenv("ERUN_RELEASE_NAMESPACE")),
		ReleaseServiceAccount: strings.TrimSpace(os.Getenv("ERUN_RELEASE_SERVICE_ACCOUNT")),
		ReleaseRuntimeVersion: strings.TrimSpace(os.Getenv("ERUN_RELEASE_RUNTIME_VERSION")),
		ReleaseHomeClaim:      strings.TrimSpace(os.Getenv("ERUN_RELEASE_HOME_CLAIM")),
		ReleaseWorkspaceClaim: strings.TrimSpace(os.Getenv("ERUN_RELEASE_WORKSPACE_CLAIM")),
		ReleaseRepoPath:       strings.TrimSpace(os.Getenv("ERUN_RELEASE_REPO_PATH")),
		ReleaseDryRun:         strings.TrimSpace(os.Getenv("ERUN_RELEASE_DRY_RUN")) == "1",

		PlatformIssuer:          strings.TrimSpace(os.Getenv("ERUN_PLATFORM_ISSUER")),
		PlatformAPIURL:          strings.TrimSpace(os.Getenv("ERUN_PLATFORM_API_URL")),
		PlatformConsoleURL:      strings.TrimSpace(os.Getenv("ERUN_PLATFORM_CONSOLE_URL")),
		PlatformConsoleClientID: strings.TrimSpace(os.Getenv("ERUN_PLATFORM_CONSOLE_CLIENT_ID")),
		PlatformCLIClientID:     strings.TrimSpace(os.Getenv("ERUN_PLATFORM_CLI_CLIENT_ID")),
		PlatformBrand:           strings.TrimSpace(os.Getenv("ERUN_PLATFORM_BRAND")),
	}
}

// optionalKubeClient builds the in-cluster Kubernetes client the env-deploy
// executor uses. A blank deployer ServiceAccount disables the executor (env
// creation only registers the row); a set SA outside a cluster is a hard
// misconfiguration (the SA is only ever set by the in-cluster deploy), so we
// fail fast rather than silently disabling.
func optionalKubeClient(cfg apiConfig) (kubernetes.Interface, error) {
	if cfg.EnvDeployerServiceAccount == "" && cfg.ReleaseServiceAccount == "" {
		return nil, nil
	}
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster config for env-deploy executor: %w", err)
	}
	return kubernetes.NewForConfig(restConfig)
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

// optionalDNS01Broker builds the DNS-01 broker when both the signing key (to
// verify per-env tokens) and the PowerDNS write path are configured. Any missing
// piece leaves the broker disabled — its endpoints are not registered — rather
// than a half-wired broker; a bad TSIG algorithm is a hard misconfiguration.
func optionalDNS01Broker(cfg apiConfig, signer *mcptoken.Signer) (*dns01broker.Broker, error) {
	if signer == nil || cfg.DNS01ServicesZone == "" || cfg.DNS01Nameserver == "" || cfg.DNS01TSIGKeyName == "" || cfg.DNS01TSIGSecret == "" {
		return nil, nil
	}
	algorithm := cfg.DNS01TSIGAlgorithm
	if algorithm == "" {
		algorithm = "hmac-sha256"
	}
	writer, err := dns01broker.NewPowerDNSWriter(cfg.DNS01Nameserver, cfg.DNS01ServicesZone, cfg.DNS01TSIGKeyName, algorithm, cfg.DNS01TSIGSecret)
	if err != nil {
		return nil, fmt.Errorf("dns01 broker: %w", err)
	}
	return dns01broker.NewBroker(signer, writer, cfg.DNS01ServicesZone, nil), nil
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
