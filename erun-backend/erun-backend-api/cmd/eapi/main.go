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
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	if len(args) > 0 && args[0] == oidcBootstrapCommand {
		return runOIDCBootstrap(context.Background(), args[1:])
	}
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
			AllowedAudiences:     splitCSV(cfg.AllowedAudiences),
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
		// Shares the DNS-01 PowerDNS writer; the adapter avoids a typed-nil interface.
		EnvironmentHostnameWriter:       environmentHostnameWriter(optional.dns01Writer),
		EnvironmentHostnameServicesZone: cfg.DNS01ServicesZone,
		KubeClient:                      optional.kubeClient,
		EnvDeploy: provision.EnvDeployConfig{
			Registry:               cfg.EnvDeployRegistry,
			PlatformNamespace:      cfg.PlatformNamespace,
			DeployerServiceAccount: cfg.EnvDeployerServiceAccount,
			ExposeTargetIP:         cfg.EnvExposeTargetIP,
			// The deploy Job's `erun expose` has no checkout, so the control plane supplies these.
			ExposeServicesZone:      cfg.DNS01ServicesZone,
			ExposePlatformNamespace: cfg.PlatformNamespace,
			ImagePullSecrets:        splitCSV(cfg.EnvDeployImagePullSecrets),
			MCPAuthPublicKeyPEM:     mcpAuthPublicKeyPEM(optional.mcpSigner),
			TLSCertSigner:           dns01TokenSigner(optional.mcpSigner),
			TLSBrokerURL:            dns01BrokerURL(cfg, optional.dns01Broker),
			TLSWebhookGroup:         cfg.DNS01WebhookGroupName,
			ACMEEmail:               cfg.ACMEEmail,
			ACMEServer:              cfg.ACMEServer,
			PlatformTenant:          cfg.BootstrapTenantName,
		},
		Platform: routes.PlatformInfo{
			Issuer:          cfg.PlatformIssuer,
			APIURL:          cfg.PlatformAPIURL,
			ConsoleURL:      cfg.PlatformConsoleURL,
			ConsoleClientID: cfg.PlatformConsoleClientID,
			CLIClientID:     cfg.PlatformCLIClientID,
			MobileClientID:  cfg.PlatformMobileClientID,
			Brand:           cfg.PlatformBrand,
			DocsURL:         cfg.PlatformDocsURL,
			Tagline:         cfg.PlatformTagline,
			LogoURL:         cfg.PlatformLogoURL,
			Version:         currentBuildVersion(),
		},
		BootstrapTenantName: cfg.BootstrapTenantName,
		IdentityAdmin:       optional.identityAdmin,
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
	log.Printf("erun api listening on %s; database=postgres audit=postgres oidc allowed issuers=%d %s", server.Addr, len(splitCSV(cfg.AllowedIssuers)), oidcAudiencePolicy(splitCSV(cfg.AllowedAudiences)))
	log.Print(identityBootstrapStatus(context.Background(), db, cfg.BootstrapTenantName))
	return server.ListenAndServe()
}

func resolveConfig(args []string) (apiConfig, error) {
	cfg := configFromEnv()
	flags := flag.NewFlagSet("eapi", flag.ContinueOnError)
	flags.Usage = func() {
		_, _ = fmt.Fprintf(flags.Output(), "Usage: eapi [flags]\n       eapi %s [flags]   (see eapi %s -h)\n\nServes the ERun backend API.\n\n", oidcBootstrapCommand, oidcBootstrapCommand)
		flags.PrintDefaults()
	}
	flags.StringVar(&cfg.Host, "host", cfg.Host, "Host interface to bind the backend API HTTP server to")
	flags.IntVar(&cfg.Port, "port", cfg.Port, "Port to bind the backend API HTTP server to")
	flags.StringVar(&cfg.DatabaseURL, "database-url", cfg.DatabaseURL, "Backend PostgreSQL database URL")
	flags.StringVar(&cfg.AllowedIssuers, "oidc-allowed-issuers", cfg.AllowedIssuers, "Comma-separated OIDC issuer allow-list; empty allows any issuer resolved from a token")
	flags.StringVar(&cfg.AllowedAudiences, "oidc-allowed-audiences", cfg.AllowedAudiences, "Comma-separated allow-list of token audiences (aud) accepted on the OIDC issuer path, typically the IdP client ids permitted to call this API; empty accepts any audience an allowed issuer minted")
	flags.StringVar(&cfg.DesktopPublicKeyPath, "desktop-public-key-path", cfg.DesktopPublicKeyPath, "Path to the desktop Ed25519 public key; when set, the API trusts file://<path> desktop-signed tokens (issue #674), the same auth the MCP edge uses")
	flags.StringVar(&cfg.MCPSigningKeyPath, "mcp-signing-key-path", cfg.MCPSigningKeyPath, "Path to the backend's Ed25519 MCP signing private key; when set, the mcp-token endpoint mints per-env MCP bearer tokens for the console (unset disables it: the endpoint reports 501)")
	flags.StringVar(&cfg.PlatformIssuer, "platform-issuer", cfg.PlatformIssuer, "This instance's OIDC issuer URL, served unauthenticated at GET /v1/platform")
	flags.StringVar(&cfg.PlatformAPIURL, "platform-api-url", cfg.PlatformAPIURL, "This instance's own API base URL, served unauthenticated at GET /v1/platform")
	flags.StringVar(&cfg.PlatformConsoleURL, "platform-console-url", cfg.PlatformConsoleURL, "This instance's hosted console URL, served unauthenticated at GET /v1/platform")
	flags.StringVar(&cfg.PlatformConsoleClientID, "platform-console-client-id", cfg.PlatformConsoleClientID, "The OIDC client id the hosted console authenticates with, served unauthenticated at GET /v1/platform")
	flags.StringVar(&cfg.PlatformCLIClientID, "platform-cli-client-id", cfg.PlatformCLIClientID, "The OIDC client id an erun CLI/agent authenticates with, served unauthenticated at GET /v1/platform")
	flags.StringVar(&cfg.PlatformMobileClientID, "platform-mobile-client-id", cfg.PlatformMobileClientID, "The OIDC client id a mobile companion client authenticates with, served unauthenticated at GET /v1/platform; empty until a mobile client's redirect URI is registered (zitadel.oidc.mobileRedirectUris)")
	flags.StringVar(&cfg.PlatformBrand, "platform-brand", cfg.PlatformBrand, "This instance's display name, served unauthenticated at GET /v1/platform")
	flags.StringVar(&cfg.PlatformDocsURL, "platform-docs-url", cfg.PlatformDocsURL, "The documentation site this instance's own surfaces link to, served unauthenticated at GET /v1/platform")
	flags.StringVar(&cfg.PlatformTagline, "platform-tagline", cfg.PlatformTagline, "The one-line pitch this instance's landing page leads with, served unauthenticated at GET /v1/platform")
	flags.StringVar(&cfg.PlatformLogoURL, "platform-logo-url", cfg.PlatformLogoURL, "Absolute URL of this instance's logo, served unauthenticated at GET /v1/platform")
	flags.StringVar(&cfg.ACMEEmail, "acme-email", cfg.ACMEEmail, "Contact email for the ACME account a deploy Job uses to provision a hosted env's per-env TLS certificate through the DNS-01 broker; unset skips TLS cert provisioning")
	flags.StringVar(&cfg.ACMEServer, "acme-server", cfg.ACMEServer, "ACME directory URL for per-env TLS certificate provisioning")
	flags.StringVar(&cfg.DNS01WebhookGroupName, "dns01-webhook-group-name", cfg.DNS01WebhookGroupName, "API group the cluster's cert-manager DNS-01 webhook shim registers under; must match the shim actually installed in this cluster")
	flags.StringVar(&cfg.ZitadelManagementAPIURL, "zitadel-management-api-url", cfg.ZitadelManagementAPIURL, "Base URL of the platform's Zitadel Management API (e.g. http://<tenant>-zitadel:8080); unset disables the /v1/identity/* routes")
	flags.StringVar(&cfg.ZitadelExternalDomain, "zitadel-external-domain", cfg.ZitadelExternalDomain, "The externally reachable host Zitadel resolves the instance from; every Management API call carries it as the outgoing Host header")
	flags.StringVar(&cfg.ZitadelManagementPATPath, "zitadel-management-pat-path", cfg.ZitadelManagementPATPath, "Path to the mounted org-owner Zitadel PAT the erun-zitadel chart provisions; never passed as a flag value in practice, only as a file path")
	if err := flags.Parse(args); err != nil {
		return apiConfig{}, err
	}
	return cfg, nil
}

// apiDependencies stay nil unless configured, disabling their feature.
type apiDependencies struct {
	cipher        *secrets.Cipher
	dbosCtx       dbos.DBOSContext
	mcpSigner     *mcptoken.Signer
	dns01Writer   *dns01broker.PowerDNSWriter
	dns01Broker   *dns01broker.Broker
	kubeClient    kubernetes.Interface
	identityAdmin *zitadel.Client
}

func optionalDependencies(cfg apiConfig) (apiDependencies, error) {
	cipher, err := whenConfigured(cfg.SecretsKey != "", func() (*secrets.Cipher, error) {
		return secrets.NewCipher(cfg.SecretsKey)
	})
	if err != nil {
		return apiDependencies{}, err
	}
	dbosCtx, err := whenConfigured(cfg.DBOSDatabaseURL != "", func() (dbos.DBOSContext, error) {
		return newDBOSContext(cfg.DBOSDatabaseURL)
	})
	if err != nil {
		return apiDependencies{}, err
	}
	mcpSigner, err := whenConfigured(cfg.MCPSigningKeyPath != "", func() (*mcptoken.Signer, error) {
		return loadMCPSigner(cfg.MCPSigningKeyPath)
	})
	if err != nil {
		return apiDependencies{}, err
	}
	dns01Writer, err := whenConfigured(dns01WriterConfigured(cfg), func() (*dns01broker.PowerDNSWriter, error) {
		return newDNS01Writer(cfg)
	})
	if err != nil {
		return apiDependencies{}, err
	}
	dns01Broker := optionalDNS01Broker(mcpSigner, dns01Writer, cfg.DNS01ServicesZone)
	kubeClient, err := whenConfigured(cfg.EnvDeployerServiceAccount != "", envDeployKubeClient)
	if err != nil {
		return apiDependencies{}, err
	}
	identityAdmin, err := optionalIdentityAdmin(cfg)
	if err != nil {
		return apiDependencies{}, err
	}
	return apiDependencies{
		cipher:        cipher,
		dbosCtx:       dbosCtx,
		mcpSigner:     mcpSigner,
		dns01Writer:   dns01Writer,
		dns01Broker:   dns01Broker,
		kubeClient:    kubeClient,
		identityAdmin: identityAdmin,
	}, nil
}

func whenConfigured[T any](configured bool, build func() (T, error)) (T, error) {
	if !configured {
		var disabled T
		return disabled, nil
	}
	return build()
}

// environmentHostnameWriter keeps "not configured" a nil interface rather than a typed nil.
func environmentHostnameWriter(writer *dns01broker.PowerDNSWriter) routes.EnvironmentHostnameWriter {
	if writer == nil {
		return nil
	}
	return writer
}

// optionalIdentityAdmin is nil unless all three settings are set; a broken PAT path is an error.
func optionalIdentityAdmin(cfg apiConfig) (*zitadel.Client, error) {
	return zitadel.NewClientFromFile(zitadel.Config{
		BaseURL:        cfg.ZitadelManagementAPIURL,
		ExternalDomain: cfg.ZitadelExternalDomain,
		PATPath:        cfg.ZitadelManagementPATPath,
	})
}

// oidcAudiencePolicy logs whether the audience boundary is enforced, since a
// count of zero would hide it.
func oidcAudiencePolicy(audiences []string) string {
	if len(audiences) == 0 {
		return "oidc audience enforcement=off (any audience from an allowed issuer is accepted)"
	}
	return fmt.Sprintf("oidc audience enforcement=on (allowed: %s)", strings.Join(audiences, ", "))
}

func identityBootstrapStatus(ctx context.Context, db *sql.DB, platformTenant string) string {
	tenants, tenantErr := countRows(ctx, db, "tenants")
	users, userErr := countRows(ctx, db, "users")
	issuers, issuerErr := countRows(ctx, db, "tenant_issuers")
	if tenantErr != nil || userErr != nil || issuerErr != nil {
		return fmt.Sprintf("erun api identity status unavailable; tenants=%s users=%s issuers=%s", countStatus(tenants, tenantErr), countStatus(users, userErr), countStatus(issuers, issuerErr))
	}
	if tenants == 0 {
		return "erun api identity bootstrap pending; firstTenant=false firstUser=false tenants=0 users=0 issuers=0"
	}
	mismatch := platformTenantNameMismatch(ctx, db, platformTenant)
	if users == 0 {
		return fmt.Sprintf("erun api identity bootstrap pending; firstTenant=true firstUser=false tenants=%d users=0 issuers=%d%s", tenants, issuers, mismatch)
	}
	return fmt.Sprintf("erun api identity ready; firstTenant=true firstUser=true tenants=%d users=%d issuers=%d%s", tenants, users, issuers, mismatch)
}

// platformTenantNameMismatch reports when ERUN_TENANT disagrees with the
// OPERATIONS tenant's bootstrapped name, which bootstrap never revisits.
func platformTenantNameMismatch(ctx context.Context, db *sql.DB, platformTenant string) string {
	platformTenant = strings.TrimSpace(platformTenant)
	if platformTenant == "" {
		return ""
	}
	var operationsName string
	err := db.QueryRowContext(ctx, `SELECT name FROM tenants WHERE type = 'OPERATIONS' ORDER BY created_at ASC LIMIT 1`).Scan(&operationsName)
	if err != nil {
		return ""
	}
	if operationsName == platformTenant {
		return ""
	}
	return fmt.Sprintf("; tenant name mismatch: declared tenant is %q, OPERATIONS tenant is %q; reconcile via PATCH /v1/tenants/reconcile-bootstrap-name", platformTenant, operationsName)
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
	Host           string
	Port           int
	DatabaseURL    string
	AllowedIssuers string
	// AllowedAudiences separates clients of one shared IdP; empty accepts any.
	AllowedAudiences     string
	DesktopPublicKeyPath string
	MCPSigningKeyPath    string
	// DNS-01 broker write path; absent disables the broker.
	DNS01ServicesZone  string
	DNS01Nameserver    string
	DNS01TSIGKeyName   string
	DNS01TSIGAlgorithm string
	DNS01TSIGSecret    string
	// Per-env TLS via the DNS-01 broker; ACMEEmail is required, the others default
	// to terraform-erun-cluster-edge's defaults.
	ACMEEmail             string
	ACMEServer            string
	DNS01WebhookGroupName string
	// Live context provisioning needs both SecretsKey and DBOSDatabaseURL (a separate
	// database). Empty AWSEndpoint means real AWS.
	SecretsKey      string
	DBOSDatabaseURL string
	AWSEndpoint     string
	// Setting EnvDeployerServiceAccount enables live env provisioning.
	EnvDeployerServiceAccount string
	PlatformNamespace         string
	EnvDeployRegistry         string
	// Set, each env-deploy Job also chains `erun expose` for the env's MCP edge.
	EnvExposeTargetIP string
	// Without pull secrets the published-image probe can never confirm an image absent.
	EnvDeployImagePullSecrets string
	// Platform is served unauthenticated at GET /v1/platform; every field is optional
	// and an empty value means the client uses its own default.
	PlatformIssuer          string
	PlatformAPIURL          string
	PlatformConsoleURL      string
	PlatformConsoleClientID string
	PlatformCLIClientID     string
	PlatformMobileClientID  string
	PlatformBrand           string
	PlatformDocsURL         string
	PlatformTagline         string
	PlatformLogoURL         string
	// BootstrapTenantName (ERUN_TENANT) names the platform tenant on empty-database
	// bootstrap, so the first <tenant>-devops resolve finds a published image.
	BootstrapTenantName string
	// All three Zitadel fields must be set for /v1/identity/* to register.
	ZitadelManagementAPIURL  string
	ZitadelExternalDomain    string
	ZitadelManagementPATPath string
}

func configFromEnv() apiConfig {
	return apiConfig{
		Host:                  envOrDefault("ERUN_API_HOST", "127.0.0.1"),
		Port:                  intEnvOrDefault("ERUN_API_PORT", 17033),
		DatabaseURL:           strings.TrimSpace(os.Getenv("ERUN_DATABASE_URL")),
		AllowedIssuers:        strings.TrimSpace(os.Getenv("ERUN_OIDC_ALLOWED_ISSUERS")),
		AllowedAudiences:      strings.TrimSpace(os.Getenv("ERUN_OIDC_ALLOWED_AUDIENCES")),
		DesktopPublicKeyPath:  strings.TrimSpace(os.Getenv("ERUN_API_DESKTOP_PUBLIC_KEY_PATH")),
		MCPSigningKeyPath:     strings.TrimSpace(os.Getenv("ERUN_API_MCP_SIGNING_KEY_PATH")),
		SecretsKey:            strings.TrimSpace(os.Getenv("ERUN_SECRETS_KEY")),
		DBOSDatabaseURL:       strings.TrimSpace(os.Getenv("DBOS_SYSTEM_DATABASE_URL")),
		AWSEndpoint:           strings.TrimSpace(os.Getenv("ERUN_AWS_ENDPOINT_URL")),
		DNS01ServicesZone:     strings.TrimSpace(os.Getenv("ERUN_DNS01_SERVICES_ZONE")),
		DNS01Nameserver:       strings.TrimSpace(os.Getenv("ERUN_DNS01_POWERDNS_NAMESERVER")),
		DNS01TSIGKeyName:      strings.TrimSpace(os.Getenv("ERUN_DNS01_TSIG_KEY_NAME")),
		DNS01TSIGAlgorithm:    strings.TrimSpace(os.Getenv("ERUN_DNS01_TSIG_ALGORITHM")),
		DNS01TSIGSecret:       strings.TrimSpace(os.Getenv("ERUN_DNS01_TSIG_SECRET")),
		ACMEEmail:             strings.TrimSpace(os.Getenv("ERUN_ACME_EMAIL")),
		ACMEServer:            envOrDefault("ERUN_ACME_SERVER", "https://acme-v02.api.letsencrypt.org/directory"),
		DNS01WebhookGroupName: envOrDefault("ERUN_DNS01_WEBHOOK_GROUP_NAME", "acme.erun.io"),

		EnvDeployerServiceAccount: strings.TrimSpace(os.Getenv("ERUN_ENV_DEPLOYER_SERVICE_ACCOUNT")),
		PlatformNamespace:         strings.TrimSpace(os.Getenv("POD_NAMESPACE")),
		EnvDeployRegistry:         envOrDefault("ERUN_ENV_DEPLOY_REGISTRY", "ghcr.io/sophium"),
		EnvExposeTargetIP:         strings.TrimSpace(os.Getenv("ERUN_ENV_EXPOSE_TARGET_IP")),
		EnvDeployImagePullSecrets: strings.TrimSpace(os.Getenv("ERUN_ENV_DEPLOY_IMAGE_PULL_SECRETS")),

		PlatformIssuer:          strings.TrimSpace(os.Getenv("ERUN_PLATFORM_ISSUER")),
		PlatformAPIURL:          strings.TrimSpace(os.Getenv("ERUN_PLATFORM_API_URL")),
		PlatformConsoleURL:      strings.TrimSpace(os.Getenv("ERUN_PLATFORM_CONSOLE_URL")),
		PlatformConsoleClientID: strings.TrimSpace(os.Getenv("ERUN_PLATFORM_CONSOLE_CLIENT_ID")),
		PlatformCLIClientID:     strings.TrimSpace(os.Getenv("ERUN_PLATFORM_CLI_CLIENT_ID")),
		PlatformMobileClientID:  strings.TrimSpace(os.Getenv("ERUN_PLATFORM_MOBILE_CLIENT_ID")),
		PlatformBrand:           strings.TrimSpace(os.Getenv("ERUN_PLATFORM_BRAND")),
		PlatformDocsURL:         strings.TrimSpace(os.Getenv("ERUN_PLATFORM_DOCS_URL")),
		PlatformTagline:         strings.TrimSpace(os.Getenv("ERUN_PLATFORM_TAGLINE")),
		PlatformLogoURL:         strings.TrimSpace(os.Getenv("ERUN_PLATFORM_LOGO_URL")),

		BootstrapTenantName: strings.TrimSpace(os.Getenv("ERUN_TENANT")),

		ZitadelManagementAPIURL:  strings.TrimSpace(os.Getenv("ERUN_ZITADEL_MANAGEMENT_API_URL")),
		ZitadelExternalDomain:    strings.TrimSpace(os.Getenv("ERUN_ZITADEL_EXTERNAL_DOMAIN")),
		ZitadelManagementPATPath: strings.TrimSpace(os.Getenv("ERUN_ZITADEL_MANAGEMENT_PAT_PATH")),
	}
}

// envDeployKubeClient fails fast when a deployer SA is set outside a cluster.
func envDeployKubeClient() (kubernetes.Interface, error) {
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster config for env-deploy executor: %w", err)
	}
	return kubernetes.NewForConfig(restConfig)
}

// loadMCPSigner returns nil for a blank path; an unreadable or invalid key is an error.
func loadMCPSigner(path string) (*mcptoken.Signer, error) {
	privatePEM, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read mcp signing key %s: %w", path, err)
	}
	return mcptoken.NewSigner(privatePEM)
}

func mcpAuthPublicKeyPEM(signer *mcptoken.Signer) string {
	if signer == nil {
		return ""
	}
	return signer.PublicKeyPEM()
}

// dns01TokenSigner returns a genuinely nil interface when no signer is configured.
func dns01TokenSigner(signer *mcptoken.Signer) provision.DNS01TokenSigner {
	if signer == nil {
		return nil
	}
	return signer
}

// dns01BrokerURL is the backend's own public API URL, since the broker is served
// here; empty unless both are configured.
func dns01BrokerURL(cfg apiConfig, broker *dns01broker.Broker) string {
	if broker == nil || cfg.PlatformAPIURL == "" {
		return ""
	}
	return strings.TrimRight(cfg.PlatformAPIURL, "/") + "/v1/dns01"
}

func dns01WriterConfigured(cfg apiConfig) bool {
	return cfg.DNS01ServicesZone != "" && cfg.DNS01Nameserver != "" && cfg.DNS01TSIGKeyName != "" && cfg.DNS01TSIGSecret != ""
}

func newDNS01Writer(cfg apiConfig) (*dns01broker.PowerDNSWriter, error) {
	algorithm := cfg.DNS01TSIGAlgorithm
	if algorithm == "" {
		algorithm = "hmac-sha256"
	}
	writer, err := dns01broker.NewPowerDNSWriter(cfg.DNS01Nameserver, cfg.DNS01ServicesZone, cfg.DNS01TSIGKeyName, algorithm, cfg.DNS01TSIGSecret)
	if err != nil {
		return nil, fmt.Errorf("dns01 writer: %w", err)
	}
	return writer, nil
}

// optionalDNS01Broker is nil unless both the signer and writer are configured.
func optionalDNS01Broker(signer *mcptoken.Signer, writer *dns01broker.PowerDNSWriter, servicesZone string) *dns01broker.Broker {
	if signer == nil || writer == nil {
		return nil
	}
	return dns01broker.NewBroker(signer, writer, servicesZone, nil)
}

func newDBOSContext(databaseURL string) (dbos.DBOSContext, error) {
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
