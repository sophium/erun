package backendapi

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/deploy"
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
	// DBOSContext + Cipher enable live context provisioning (issue #605/#676):
	// when both are set (alongside DB), POST /v1/contexts starts a durable DBOS
	// workflow that runs the real bootstrap and custodies the k3s token. When
	// absent, context creation only registers the row (no live bootstrap).
	DBOSContext dbos.DBOSContext
	Cipher      *secrets.Cipher
	// AWSEndpoint pins provisioning's aws calls at a local emulator (floci) for
	// verification; empty means real AWS.
	AWSEndpoint string
	// RuntimeRegistry is where the published runtime chart + image live; the
	// env-deploy executor addresses oci://<RuntimeRegistry>/charts/erun-devops
	// and pulls the runtime image from there. Empty defaults to ghcr.io/sophium.
	RuntimeRegistry string
	// EnvDeployChartPath / EnvDeployImage / EnvDeployNoWait pin the env-deploy
	// executor's chart source, image, and wait behaviour at local/test values so
	// the durable deploy workflow can be exercised against a throwaway cluster
	// (Lima k3s) without the published OCI chart or the ~1GB runtime image
	// (mirrors AWSEndpoint for provisioning). Zero values = production: published
	// OCI chart, chart-default images, helm waits for the rollout.
	EnvDeployChartPath string
	EnvDeployImage     string
	EnvDeployNoWait    bool
	// EnvDeployRegistryPlainHTTP makes the deploy helm OCI client use plain HTTP
	// for a local registry (verification). Zero value = production (HTTPS).
	EnvDeployRegistryPlainHTTP bool
	// MCPSigningKeyPath persists the backend's Ed25519 MCP-signing identity
	// (issue #686): when set, POST /v1/environments/{id}/mcp-token mints per-env
	// MCP bearers signed by this key (the deploy injects the matching public key
	// into each env). Empty disables minting (the endpoint returns 501).
	MCPSigningKeyPath string
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
		// One MCP-signing identity drives both sides of #686: the deployer injects
		// its public key into each env, and the mcp-token route signs with it.
		// Keep nil-when-unconfigured as typed nil so the interface stays nil (a nil
		// *Signer in a non-nil interface would pass != nil and panic on use).
		var mcpKeys deploy.MCPKeyProvider
		var mcpSigner routes.MCPTokenSigner
		if keyPath := strings.TrimSpace(options.MCPSigningKeyPath); keyPath != "" {
			signer := mcptoken.NewSigner(keyPath)
			mcpKeys = signer
			mcpSigner = signer
		}
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
		var contextProvisioner routes.ContextProvisioner
		var environmentDeployer routes.EnvironmentDeployer
		if options.Cipher != nil {
			aliases := repository.NewCloudProviderAliasRepository(txManager, options.Cipher)
			routes.RegisterCloudProviderAliasRoutes(register, aliases)
			if options.DBOSContext != nil {
				credentials := repository.NewContextCredentialRepository(txManager, options.Cipher)
				contextProvisioner = provision.NewProvisioner(
					options.DBOSContext,
					contexts,
					credentials,
					aliases,
					options.Cipher,
					options.AWSEndpoint,
				)
				environmentDeployer = deploy.NewEnvDeployer(
					options.DBOSContext,
					environments,
					contexts,
					credentials,
					tenants,
					mcpKeys,
					deploy.EnvDeployOptions{
						RuntimeRegistry:   options.RuntimeRegistry,
						ChartPathOverride: options.EnvDeployChartPath,
						ImageOverride:     options.EnvDeployImage,
						NoWait:            options.EnvDeployNoWait,
						RegistryPlainHTTP: options.EnvDeployRegistryPlainHTTP,
					},
				)
			}
		}
		routes.RegisterEnvironmentRoutes(register, environments, tenantQuotas, contexts, environmentDeployer, mcpSigner, tenants)
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
