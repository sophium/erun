package backendapi

import (
	"database/sql"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/routes"
)

type HandlerOptions struct {
	TokenVerifier    TokenVerifier
	IdentityResolver IdentityResolver
	TenantResolver   TenantResolver
	UserResolver     UserResolver
	IdentityCache    *IdentityResolutionCache
	AuditLogger      AuditLogger
	Authorizer       Authorizer
	DB               *sql.DB
	DBDialect        repository.Dialect
	AuditDB          *sql.DB
	AuditDialect     repository.Dialect
}

func NewHandler(options HandlerOptions) (http.Handler, error) {
	identityResolver := options.IdentityResolver
	tenantResolver := options.TenantResolver
	userResolver := options.UserResolver
	if options.DB != nil && (tenantResolver == nil || userResolver == nil) {
		identities := repository.NewIdentityRepository(options.DB, options.DBDialect)
		if identityResolver == nil && tenantResolver == nil && userResolver == nil {
			identityResolver = identities
		} else if tenantResolver == nil {
			tenantResolver = identities
		}
		if userResolver == nil {
			userResolver = identities
		}
	}
	audit := options.AuditLogger
	auditDB := options.AuditDB
	auditDialect := options.AuditDialect
	if auditDB == nil && options.DB != nil && options.DBDialect != repository.DialectPostgres {
		auditDB = options.DB
		auditDialect = options.DBDialect
	}
	if audit == nil && auditDB != nil {
		audit = repository.NewAuditEventRepositoryForDialect(auditDB, auditDialect)
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
		IdentityCache:    options.IdentityCache,
		AuditLogger:      audit,
		Authorizer:       authorizer,
	})
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	register := protectedRouteRegistrar(mux, auth)
	routes.RegisterWhoamiRoute(register)
	if options.DB != nil {
		txManager := repository.NewTxManager(options.DB, options.DBDialect)
		reviews := repository.NewReviewRepository(txManager)
		builds := repository.NewBuildRepository(txManager)
		comments := repository.NewCommentRepository(txManager)
		routes.RegisterReviewRoutes(register, reviews)
		routes.RegisterBuildRoutes(register, builds)
		routes.RegisterCommentRoutes(register, comments)
	}
	return mux, nil
}

func registerProtectedRoute(mux *http.ServeMux, auth *AuthMiddleware, method string, apiPath string, handler http.Handler) {
	mux.Handle(method+" "+apiPath, withAPIPath(apiPath, auth.Wrap(handler)))
}

func protectedRouteRegistrar(mux *http.ServeMux, auth *AuthMiddleware) routes.ProtectedRouteRegistrar {
	return func(method string, apiPath string, handler http.Handler) {
		registerProtectedRoute(mux, auth, method, apiPath, handler)
	}
}
