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
		KubeClient:    optional.kubeClient,
		EnvDeploy: provision.EnvDeployConfig{
			Registry:               cfg.EnvDeployRegistry,
			PlatformNamespace:      cfg.PlatformNamespace,
			DeployerServiceAccount: cfg.EnvDeployerServiceAccount,
			ExposeTargetIP:         cfg.EnvExposeTargetIP,
			// The deploy Job's chained `erun expose` has no git checkout to resolve
			// these from, so the control plane threads what it already
			// knows for its own purposes: DNS01ServicesZone is the same services
			// zone its DNS-01 cert issuance uses, and PlatformNamespace is where its
			// own Jobs (and, in a self-hosted platform, the PowerDNS singleton) run.
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

// resolveConfig layers command-line flags over the environment-derived config.
func resolveConfig(args []string) (apiConfig, error) {
	cfg := configFromEnv()
	flags := flag.NewFlagSet("eapi", flag.ContinueOnError)
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

// apiDependencies are the runtime pieces the API only gets when their
// configuration is present; each stays nil otherwise, disabling its feature.
type apiDependencies struct {
	cipher        *secrets.Cipher
	dbosCtx       dbos.DBOSContext
	mcpSigner     *mcptoken.Signer
	dns01Broker   *dns01broker.Broker
	kubeClient    kubernetes.Interface
	identityAdmin *zitadel.Client
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
	identityAdmin, err := optionalIdentityAdmin(cfg)
	if err != nil {
		return apiDependencies{}, err
	}
	return apiDependencies{
		cipher:        cipher,
		dbosCtx:       dbosCtx,
		mcpSigner:     mcpSigner,
		dns01Broker:   dns01Broker,
		kubeClient:    kubeClient,
		identityAdmin: identityAdmin,
	}, nil
}

// optionalIdentityAdmin wires the Zitadel Management API client (issue
// #1209). Any of the three settings missing leaves it nil -- the
// /v1/identity/* routes then do not register at all -- but a set-but-broken
// PAT path (unreadable, empty) is a hard misconfiguration, matching every
// other optional dependency's convention in this file.
func optionalIdentityAdmin(cfg apiConfig) (*zitadel.Client, error) {
	return zitadel.NewClientFromFile(zitadel.Config{
		BaseURL:        cfg.ZitadelManagementAPIURL,
		ExternalDomain: cfg.ZitadelExternalDomain,
		PATPath:        cfg.ZitadelManagementPATPath,
	})
}

// oidcAudiencePolicy states the audience boundary in the startup log instead of
// leaving an operator to read it out of a count of zero: an unenforced boundary
// and one that happens to be passing look identical in the logs otherwise, and
// which of the two a deployment is in is exactly what an operator needs to know.
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

// platformTenantNameMismatch reports when this instance's declared tenant
// (ERUN_TENANT) disagrees with the name its own OPERATIONS tenant actually
// bootstrapped under. Bootstrap only ever runs once, so a platform
// whose ERUN_TENANT was unset (or different) at bootstrap time can carry that
// original name indefinitely with nothing else to say so -- the mismatch was
// previously discoverable only by querying the database directly. Empty when
// ERUN_TENANT is unset, the OPERATIONS tenant cannot be read, or the two
// already agree.
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
	// AllowedAudiences narrows the OIDC path to tokens minted for named
	// audiences — the boundary between clients of one shared IdP, which the
	// issuer allow-list cannot draw. Empty accepts any audience, so a deployment
	// that has not established which client ids its IdP mints behaves as it
	// always did; the startup log reports which of the two applies.
	AllowedAudiences     string
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
	// ACMEEmail/ACMEServer/DNS01WebhookGroupName configure the per-env TLS
	// certificate a deploy Job provisions through the DNS-01 broker: the
	// same ACME account and per-cluster webhook shim
	// (terraform-erun-cluster-edge's chart-dns01-webhook) the platform's own
	// per-env certificate already uses. ACMEEmail is required for a deploy Job
	// to attempt TLS provisioning at all; ACMEServer and DNS01WebhookGroupName
	// default to Let's Encrypt production and the webhook shim's own default
	// group name, matching that module's defaults.
	ACMEEmail             string
	ACMEServer            string
	DNS01WebhookGroupName string
	// These enable optional live context provisioning; it stays disabled unless
	// SecretsKey and DBOSDatabaseURL are both set. DBOSDatabaseURL is a separate
	// database from ERUN_DATABASE_URL. AWSEndpoint targets a local emulator for
	// verification (empty = real AWS).
	SecretsKey      string
	DBOSDatabaseURL string
	AWSEndpoint     string
	// Server-side env-deploy executor. EnvDeployerServiceAccount is the
	// cluster-admin SA the deploy Job runs as; setting it enables live env
	// provisioning (which then also needs an in-cluster kube client and
	// DBOSContext). PlatformNamespace is the namespace the Jobs run in (the
	// backend's own, via the downward API). EnvDeployRegistry is the image
	// registry the tenant's <tenant>-devops runtime image is pulled from.
	EnvDeployerServiceAccount string
	PlatformNamespace         string
	EnvDeployRegistry         string
	// EnvExposeTargetIP is the platform's ingress IP: set, every env-deploy Job
	// also chains an `erun expose` for the env's MCP edge after a successful
	// deploy; unset (the default), deploys stay exactly as they were before —
	// no attempt to expose, independent of whether the executor above is on.
	EnvExposeTargetIP string
	// EnvDeployImagePullSecrets names the dockerconfigjson secrets in the
	// platform namespace the deploy Job pulls with. The published-image probe
	// reads them so it can interrogate a private registry namespace with the
	// same credential; unset, the probe stays unauthenticated and can never
	// confirm an image absent, so no deploy is diverted to the canonical image.
	EnvDeployImagePullSecrets string
	// Platform is this instance's own self-describing config, served
	// unauthenticated at GET /v1/platform so a client can discover it (issuer,
	// API/console URLs, OIDC client ids, brand, and the white-label docs
	// URL/tagline/logo its front door renders) before it has a token. Every
	// field is optional; an absent value renders as an empty string, never an
	// error, and a client falls back to its own bundled default.
	// ConsoleClientID/CLIClientID/MobileClientID are typically sourced from the
	// erun-zitadel bootstrap's published ConfigMap, threaded in by the chart.
	// MobileClientID is empty until an operator configures a mobile client's
	// redirect URI (zitadel.oidc.mobileRedirectUris) -- no erun-mobile app
	// exists in the IdP until then.
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
	// BootstrapTenantName is this pod's own declared tenant identity
	// (ERUN_TENANT: the tenant this control plane runs as, e.g. "frs" on
	// erunpaas.com). Empty-database bootstrap enrols the platform's own
	// tenant under this name so hosted provisioning's first resolve of
	// <tenant>-devops finds an image the platform actually publishes,
	// instead of a synthetic placeholder falling back only when it is unset.
	BootstrapTenantName string
	// Identity administration drives Zitadel's Management API
	// with the org-owner PAT the erun-zitadel chart already provisions and
	// persists. All three fields must be set for the /v1/identity/* routes
	// to register; any one missing leaves them off, same as every other
	// optional dependency in this config.
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

// optionalKubeClient builds the in-cluster Kubernetes client the env-deploy
// executor needs. A blank deployer ServiceAccount disables it (env creation
// only registers the row); a set SA outside a cluster is a hard
// misconfiguration (the SA is only ever set by the in-cluster deploy), so we
// fail fast rather than silently disabling.
func optionalKubeClient(cfg apiConfig) (kubernetes.Interface, error) {
	if cfg.EnvDeployerServiceAccount == "" {
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

// mcpAuthPublicKeyPEM returns the backend's own MCP-signing public key when a
// signer is configured, empty otherwise — threaded into every deploy Job so
// the runtime's MCP edge trusts backend-minted tokens.
func mcpAuthPublicKeyPEM(signer *mcptoken.Signer) string {
	if signer == nil {
		return ""
	}
	return signer.PublicKeyPEM()
}

// dns01TokenSigner adapts the backend's MCP signer to provision.DNS01TokenSigner
// for per-env TLS cert provisioning, returning a genuinely nil
// interface when no signer is configured. Assigning a nil *mcptoken.Signer
// directly to the interface field would produce a non-nil interface wrapping a
// nil pointer, which applyTLSCertParams's nil check would miss.
func dns01TokenSigner(signer *mcptoken.Signer) provision.DNS01TokenSigner {
	if signer == nil {
		return nil
	}
	return signer
}

// dns01BrokerURL derives the DNS-01 broker's base URL from this instance's own
// public API URL: the broker's present/cleanup endpoints are served on this
// same backend (dns01broker.Broker.Register), so a per-env Issuer's webhook
// solver reaches it exactly as any other client would. Empty unless both the
// platform API URL and the broker itself are configured, since a per-env
// Issuer pointed at an unconfigured broker would never solve.
func dns01BrokerURL(cfg apiConfig, broker *dns01broker.Broker) string {
	if broker == nil || cfg.PlatformAPIURL == "" {
		return ""
	}
	return strings.TrimRight(cfg.PlatformAPIURL, "/") + "/v1/dns01"
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
