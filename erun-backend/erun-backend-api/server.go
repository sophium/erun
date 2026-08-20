package backendapi

import (
	"database/sql"
	"log"
	"net/http"
	"strings"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"k8s.io/client-go/kubernetes"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/deployexec"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/dns01broker"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/mcptoken"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/provision"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/releaseexec"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/routes"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/secrets"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

type HandlerOptions struct {
	TokenVerifier    TokenVerifier
	IdentityResolver IdentityResolver
	TenantResolver   TenantResolver
	UserResolver     UserResolver
	OrgResolver      OrgResolver
	IdentityCache    *IdentityResolutionCache
	AuditLogger      AuditLogger
	Authorizer       Authorizer
	DB               *sql.DB
	DBDialect        repository.Dialect
	// With both set (and DB), context creation runs the real provisioning
	// bootstrap; without them it only registers the context row.
	DBOSContext dbos.DBOSContext
	Cipher      *secrets.Cipher
	// AWSEndpoint pins provisioning's aws calls at a local emulator (floci) for
	// verification; empty means real AWS.
	AWSEndpoint string
	// MCPSigner mints per-env MCP bearer tokens for the console. Nil when no
	// backend MCP signing key is configured; the mcp-token endpoint then reports
	// 501 rather than minting an unverifiable token. It also signs and verifies
	// the per-env DNS-01 broker tokens (same key, distinct audience).
	MCPSigner *mcptoken.Signer
	// DNS01Broker serves the authenticated DNS-01 present/cleanup endpoints. Nil
	// when the PowerDNS write path is not configured; the endpoints are then not
	// registered (a cluster with no brokered DNS-01 solver never calls them).
	DNS01Broker *dns01broker.Broker
	// KubeClient runs the server-side env-deploy Jobs. Nil (the default outside a
	// cluster) leaves env provisioning off: POST /v1/environments only registers
	// the row. Set together with EnvDeploy and DBOSContext to enable live deploys.
	KubeClient kubernetes.Interface
	// EnvDeploy is the per-instance placement for env-deploy Jobs (image registry,
	// platform namespace, cluster-admin deployer ServiceAccount). Env provisioning
	// stays off until all three are set.
	EnvDeploy provision.EnvDeployConfig
	// Release is the per-instance placement for release Jobs: the agent
	// environment whose warm caches the release runs beside, its ServiceAccount,
	// and the runtime image version. Unset leaves the release queue recording
	// triggers without running them.
	Release provision.ReleaseConfig
	// Platform is this instance's own self-describing config, served
	// unauthenticated at GET /v1/platform so a client can discover it before it
	// has a token. Unset fields render as empty strings, never as an error.
	Platform routes.PlatformInfo
}

func NewHandler(options HandlerOptions) (http.Handler, error) {
	var txManager *repository.TxManager
	if options.DB != nil {
		txManager = repository.NewTxManager(options.DB, options.DBDialect)
	}
	auth, err := newAuthMiddlewareFor(options, txManager)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	registerHealthRoute(mux)
	// A client needs this instance's own config (issuer, client ids, ...) before
	// it has a token to authenticate with, so it is registered unauthenticated,
	// directly on the mux, next to the health check.
	routes.RegisterPlatformRoute(mux, options.Platform)
	// The DNS-01 broker authenticates its own per-env M2M token (not a user OIDC
	// token), so it is registered directly on the mux rather than behind the
	// user-auth/authorize/audit middleware the protected routes use.
	if options.DNS01Broker != nil {
		options.DNS01Broker.Register(mux)
	}
	register := protectedRouteRegistrar(mux, auth)
	var users routes.WhoamiUserRepository
	if txManager != nil {
		users = repository.NewUserRepository(txManager)
		registerDatabaseRoutes(register, options, txManager)
	}
	routes.RegisterWhoamiRoute(register, users)
	return mux, nil
}

// identityResolvers is the auth resolver set. Whichever ones the caller did not
// inject default to the database-backed identity repository.
type identityResolvers struct {
	identity IdentityResolver
	tenant   TenantResolver
	user     UserResolver
	org      OrgResolver
}

func resolveIdentityResolvers(options HandlerOptions) identityResolvers {
	resolvers := identityResolvers{
		identity: options.IdentityResolver,
		tenant:   options.TenantResolver,
		user:     options.UserResolver,
		org:      options.OrgResolver,
	}
	if options.DB == nil || resolvers.complete() {
		return resolvers
	}
	resolvers.defaultTo(repository.NewIdentityRepository(options.DB, options.DBDialect))
	return resolvers
}

func (r identityResolvers) complete() bool {
	return r.tenant != nil && r.user != nil && r.org != nil
}

// defaultTo fills the unset resolvers from the identity repository. A caller who
// injected no tenant or user resolver gets the combined identity resolver,
// because empty-database bootstrap must resolve or create tenant, issuer, and
// user atomically.
func (r *identityResolvers) defaultTo(identities *repository.IdentityRepository) {
	if r.identity == nil && r.tenant == nil && r.user == nil {
		r.identity = identities
	} else if r.tenant == nil {
		r.tenant = identities
	}
	if r.user == nil {
		r.user = identities
	}
	if r.org == nil {
		r.org = identities
	}
}

// newAuthMiddlewareFor assembles the authentication middleware, defaulting every
// database-backed dependency the caller did not inject.
func newAuthMiddlewareFor(options HandlerOptions, txManager *repository.TxManager) (*AuthMiddleware, error) {
	resolvers := resolveIdentityResolvers(options)
	audit := options.AuditLogger
	if audit == nil && txManager != nil {
		audit = repository.NewAuditEventRepository(txManager)
	}
	authorizer := options.Authorizer
	if authorizer == nil && options.DB != nil {
		authorizer = repository.NewPermissionAuthorizerForDialect(options.DB, options.DBDialect)
	}
	return NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier:    options.TokenVerifier,
		IdentityResolver: resolvers.identity,
		TenantResolver:   resolvers.tenant,
		UserResolver:     resolvers.user,
		OrgResolver:      resolvers.org,
		IdentityCache:    options.IdentityCache,
		AuditLogger:      audit,
		Authorizer:       authorizer,
	})
}

// registerDatabaseRoutes registers every route backed by persistence, which is
// all of them except the health check and the DNS-01 broker.
func registerDatabaseRoutes(register routes.ProtectedRouteRegistrar, options HandlerOptions, txManager *repository.TxManager) {
	reviews := repository.NewReviewRepository(txManager)
	builds := repository.NewBuildRepository(txManager)
	comments := repository.NewCommentRepository(txManager)
	tenantIssuers := repository.NewTenantIssuerRepository(txManager)
	tenants := repository.NewTenantRepository(txManager)
	environments := repository.NewEnvironmentRepository(txManager)
	contexts := repository.NewContextRepository(txManager)
	tenantQuotas := repository.NewTenantQuotaRepository(txManager)
	usageEvents := repository.NewUsageEventRepository(txManager)
	releases := repository.NewReleaseRepository(txManager)
	reviewService := service.NewReviewService(reviews, builds)
	buildService := service.NewBuildService(builds, reviewService)
	commentService := service.NewCommentService(comments)
	releaseService := service.NewReleaseService(releases, buildService, newReleaseRunner(options))
	releaseRoutes := routes.RegisterReleaseRoutes(register, releases, releaseService, newReleaseQueue(options, releaseService), tenants)
	routes.RegisterTenantIssuerRoutes(register, tenantIssuers)
	routes.RegisterReviewRoutes(register, reviews, reviewService, builds, releaseRoutes)
	routes.RegisterBuildRoutes(register, builds, buildService)
	routes.RegisterCommentRoutes(register, comments, commentService)
	routes.RegisterEnvironmentRoutes(register, environments, tenantQuotas, tenants, newEnvironmentProvisioner(options, environments, usageEvents), newEnvironmentLifecycle(options, environments, usageEvents))
	routes.RegisterUsageEventRoutes(register, usageEvents)
	routes.RegisterMCPTokenRoutes(register, environments, tenants, options.MCPSigner)
	routes.RegisterDNS01TokenRoutes(register, environments, tenants, options.MCPSigner)
	var contextProvisioner routes.ContextProvisioner
	if options.Cipher != nil {
		aliases := repository.NewCloudProviderAliasRepository(txManager, options.Cipher)
		routes.RegisterCloudProviderAliasRoutes(register, aliases)
		if options.DBOSContext != nil {
			contextProvisioner = provision.NewProvisioner(
				options.DBOSContext,
				contexts,
				repository.NewContextCredentialRepository(txManager, options.Cipher),
				aliases,
				options.Cipher,
				options.AWSEndpoint,
			)
		}
	}
	routes.RegisterContextRoutes(register, contexts, contextProvisioner)
	routes.RegisterTenantRoutes(register, tenants)
	routes.RegisterTenantQuotaRoute(register, tenantQuotas)
	routes.RegisterConfigRoute(register, tenants, environments, contexts)
	routes.RegisterProvisionRoute(register, tenants, environments, tenantQuotas)
	routes.RegisterUserRoutes(register, repository.NewUserRepository(txManager))
}

// newEnvironmentProvisioner wires live env provisioning, which needs durable
// workflows, an in-cluster client, and the full deploy placement. Anything
// missing leaves it nil, so env creation only registers the row. Without a
// startup log naming which precondition failed, an operator sees only a 501
// at call time with no way to tell which of the five is unmet.
func newEnvironmentProvisioner(options HandlerOptions, environments *repository.EnvironmentRepository, usage *repository.UsageEventRepository) routes.EnvironmentProvisioner {
	deploy := options.EnvDeploy
	if reasons := missingEnvProvisionerConfig(options, deploy); len(reasons) > 0 {
		log.Printf("erun api live env provisioning disabled: %s", strings.Join(reasons, "; "))
		return nil
	}
	coordinator := service.NewEnvironmentProvisioner(deployexec.NewLauncher(options.KubeClient), environments, usage)
	// The published-image probe runs with the deploy Job's own pull credential,
	// read from the platform namespace, so a private registry namespace answers
	// it decisively instead of identically for absent and forbidden.
	credentials := provision.NewKubeImagePullSecretCredentials(options.KubeClient, deploy.PlatformNamespace, deploy.ImagePullSecrets)
	return provision.NewEnvProvisioner(options.DBOSContext, coordinator, deploy, provision.NewGHCRImageChecker(credentials))
}

// missingEnvProvisionerConfig names every unmet precondition for live env
// provisioning, so the startup log can point at exactly what is missing
// rather than leaving an operator to infer it from a 501 at call time.
func missingEnvProvisionerConfig(options HandlerOptions, deploy provision.EnvDeployConfig) []string {
	var reasons []string
	if options.DBOSContext == nil {
		reasons = append(reasons, "DBOS_SYSTEM_DATABASE_URL is not set")
	}
	if options.KubeClient == nil {
		reasons = append(reasons, "no in-cluster Kubernetes client (the env-deployer service account is not set)")
	}
	if deploy.DeployerServiceAccount == "" {
		reasons = append(reasons, "the env-deployer service account is not set")
	}
	if deploy.PlatformNamespace == "" {
		reasons = append(reasons, "the platform namespace is not set")
	}
	if deploy.Registry == "" {
		reasons = append(reasons, "the env-deploy image registry is not set")
	}
	return reasons
}

// newEnvironmentLifecycle wires live stop/delete, which needs an in-cluster
// client and the full deploy placement but no durable workflow (see
// provision.EnvLifecycle). Anything missing leaves it nil, so stop/delete
// report the executor as unconfigured rather than acting on partial config.
func newEnvironmentLifecycle(options HandlerOptions, environments *repository.EnvironmentRepository, usage *repository.UsageEventRepository) routes.EnvironmentLifecycle {
	deploy := options.EnvDeploy
	if options.KubeClient == nil ||
		deploy.DeployerServiceAccount == "" || deploy.PlatformNamespace == "" || deploy.Registry == "" {
		return nil
	}
	return provision.NewEnvLifecycle(deployexec.NewLauncher(options.KubeClient), environments, deploy, usage)
}

// newReleaseRunner wires the release Job launcher. Without an in-cluster client
// there is nothing to launch, so the queue records triggers and the service
// reports the missing executor rather than a release that silently never ran.
func newReleaseRunner(options HandlerOptions) service.ReleaseRunner {
	if options.KubeClient == nil {
		return nil
	}
	return releaseexec.NewLauncher(options.KubeClient)
}

// newReleaseQueue wires the durable release workflow, which needs DBOS, an
// in-cluster client, and the full release placement. Anything missing leaves the
// dispatcher nil: triggers are still recorded and are runnable once the queue is
// configured.
func newReleaseQueue(options HandlerOptions, coordinator provision.ReleaseCoordinator) routes.ReleaseDispatcher {
	if options.DBOSContext == nil || options.KubeClient == nil || !options.Release.Configured() {
		return nil
	}
	return provision.NewReleaseQueue(options.DBOSContext, coordinator, options.Release)
}

func registerHealthRoute(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
}

func registerProtectedRoute(mux *http.ServeMux, auth *AuthMiddleware, method string, apiPath string, handler http.Handler) {
	mux.Handle(method+" "+apiPath, withAPIPath(apiPath, auth.Wrap(handler)))
}

func protectedRouteRegistrar(mux *http.ServeMux, auth *AuthMiddleware) routes.ProtectedRouteRegistrar {
	return func(method string, apiPath string, handler http.Handler) {
		registerProtectedRoute(mux, auth, method, apiPath, handler)
	}
}
