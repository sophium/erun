package backendapi

import (
	"database/sql"
	"net/http"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

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
	// DBOSContext + Cipher enable live context provisioning:
	// when both are set (alongside DB), POST /v1/contexts starts a durable DBOS
	// workflow that runs the real bootstrap and custodies the k3s token. When
	// absent, context creation only registers the row (no live bootstrap).
	DBOSContext dbos.DBOSContext
	Cipher      *secrets.Cipher
	// AWSEndpoint pins provisioning's aws calls at a local emulator (floci) for
	// verification; empty means real AWS.
	AWSEndpoint string
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
		routes.RegisterEnvironmentRoutes(register, environments, tenantQuotas)
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
