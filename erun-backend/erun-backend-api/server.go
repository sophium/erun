package backendapi

import (
	"database/sql"
	"net/http"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"k8s.io/client-go/kubernetes"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/deployexec"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/dns01broker"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/mcptoken"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/provision"
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
}

func NewHandler(options HandlerOptions) (http.Handler, error) {
	var txManager *repository.TxManager
	if options.DB != nil {
		txManager = repository.NewTxManager(options.DB, options.DBDialect)
	}
	identityResolver := options.IdentityResolver
	tenantResolver := options.TenantResolver
	userResolver := options.UserResolver
	orgResolver := options.OrgResolver
	if options.DB != nil && (tenantResolver == nil || userResolver == nil || orgResolver == nil) {
		identities := repository.NewIdentityRepository(options.DB, options.DBDialect)
		if identityResolver == nil && tenantResolver == nil && userResolver == nil {
			identityResolver = identities
		} else if tenantResolver == nil {
			tenantResolver = identities
		}
		if userResolver == nil {
			userResolver = identities
		}
		if orgResolver == nil {
			orgResolver = identities
		}
	}
	audit := options.AuditLogger
	if audit == nil && txManager != nil {
		audit = repository.NewAuditEventRepository(txManager)
	}
	authorizer := options.Authorizer
	if authorizer == nil && options.DB != nil {
		authorizer = repository.NewPermissionAuthorizerForDialect(options.DB, options.DBDialect)
	}
	auth, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier:    options.TokenVerifier,
		IdentityResolver: identityResolver,
		TenantResolver:   tenantResolver,
		UserResolver:     userResolver,
		OrgResolver:      orgResolver,
		IdentityCache:    options.IdentityCache,
		AuditLogger:      audit,
		Authorizer:       authorizer,
	})
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	registerHealthRoute(mux)
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
		reviews := repository.NewReviewRepository(txManager)
		builds := repository.NewBuildRepository(txManager)
		comments := repository.NewCommentRepository(txManager)
		tenantIssuers := repository.NewTenantIssuerRepository(txManager)
		tenants := repository.NewTenantRepository(txManager)
		environments := repository.NewEnvironmentRepository(txManager)
		contexts := repository.NewContextRepository(txManager)
		tenantQuotas := repository.NewTenantQuotaRepository(txManager)
		reviewService := service.NewReviewService(reviews, builds)
		buildService := service.NewBuildService(builds, reviewService)
		commentService := service.NewCommentService(comments)
		routes.RegisterTenantIssuerRoutes(register, tenantIssuers)
		routes.RegisterReviewRoutes(register, reviews, reviewService)
		routes.RegisterBuildRoutes(register, builds, buildService)
		routes.RegisterCommentRoutes(register, comments, commentService)
		var environmentProvisioner routes.EnvironmentProvisioner
		if options.DBOSContext != nil && options.KubeClient != nil &&
			options.EnvDeploy.DeployerServiceAccount != "" && options.EnvDeploy.PlatformNamespace != "" && options.EnvDeploy.Registry != "" {
			coordinator := service.NewEnvironmentProvisioner(deployexec.NewLauncher(options.KubeClient), environments)
			environmentProvisioner = provision.NewEnvProvisioner(options.DBOSContext, coordinator, options.EnvDeploy)
		}
		routes.RegisterEnvironmentRoutes(register, environments, tenantQuotas, tenants, environmentProvisioner)
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
	}
	routes.RegisterWhoamiRoute(register, users)
	return mux, nil
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
