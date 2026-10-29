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
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/gitverify"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/mcptoken"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/provision"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/registrytoken"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/routes"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/secrets"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
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
	// Capabilities answers whoami's capability set; unset defaults to the Authorizer when it
	// can resolve one, so the answer and enforcement share one source.
	Capabilities routes.WhoamiCapabilityResolver
	DB           *sql.DB
	DBDialect    repository.Dialect
	// With both set (and DB), context creation runs the real provisioning bootstrap.
	DBOSContext dbos.DBOSContext
	Cipher      *secrets.Cipher
	// AWSEndpoint points provisioning at a local emulator; empty means real AWS.
	AWSEndpoint string
	// MCPSigner mints per-env MCP and DNS-01 broker tokens; nil makes mcp-token answer 501.
	MCPSigner *mcptoken.Signer
	// DNS01Broker serves DNS-01 present/cleanup; nil leaves those endpoints unregistered.
	DNS01Broker *dns01broker.Broker
	// EnvironmentHostnameWriter backs the environment hostname route; nil answers 501.
	EnvironmentHostnameWriter routes.EnvironmentHostnameWriter
	// EnvironmentHostnameServicesZone matches DNS01Broker's zone.
	EnvironmentHostnameServicesZone string
	// KubeClient runs env-deploy Jobs; live deploys need it with EnvDeploy and DBOSContext.
	KubeClient kubernetes.Interface
	// EnvDeploy is the per-instance placement for env-deploy Jobs.
	EnvDeploy provision.EnvDeployConfig
	// Platform is served unauthenticated at GET /v1/platform.
	Platform routes.PlatformInfo
	// BootstrapTenantName (ERUN_TENANT) names the tenant empty-database bootstrap enrols.
	BootstrapTenantName string
	// IdentityAdmin drives Zitadel's Management API; nil leaves /v1/identity/* unregistered.
	IdentityAdmin *zitadel.Client
}

func NewHandler(options HandlerOptions) (http.Handler, error) {
	var txManager *repository.TxManager
	if options.DB != nil {
		txManager = repository.NewTxManager(options.DB, options.DBDialect)
	}
	authorizer := resolveAuthorizer(options)
	auth, err := newAuthMiddlewareFor(options, txManager, authorizer)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	registerHealthRoute(mux)
	// Clients need this before they have a token.
	routes.RegisterPlatformRoute(mux, options.Platform)
	// The broker authenticates its own per-env M2M token, not a user OIDC token.
	if options.DNS01Broker != nil {
		options.DNS01Broker.Register(mux)
	}
	// The registry token service authenticates HTTP Basic (the erun-api token as password),
	// and resolves the tenant the same way primary auth does.
	if resolvers := resolveIdentityResolvers(options); options.MCPSigner != nil && options.TokenVerifier != nil && resolvers.tenant != nil {
		registrytoken.NewHandler(options.TokenVerifier, resolvers.tenant, options.MCPSigner).Register(mux)
	}
	// The request body token is the credential; needs Zitadel like identity administration.
	if txManager != nil && options.IdentityAdmin != nil {
		inviteService := service.NewInviteService(repository.NewInviteRepository(txManager), options.IdentityAdmin, repository.NewUserRepository(txManager))
		routes.RegisterInviteAcceptRoute(mux, inviteService)
	}
	// The requester is authenticated but enrolled nowhere, which the protected registrar would reject.
	if txManager != nil {
		routes.RegisterInviteRequestPublicRoutes(
			mux,
			options.TokenVerifier,
			repository.NewInviteRequestRepository(txManager),
			routes.NewInviteRequestRateLimiter(repository.NewPlatformRateLimitRepository(txManager)),
		)
	}
	registerProtectedRoutes(mux, auth, options, txManager, authorizer)
	return http.MaxBytesHandler(mux, maxRequestBodyBytes), nil
}

// maxRequestBodyBytes bounds every request body, since public routes such as
// invite acceptance decode theirs before anything is trusted. Every route takes a
// small JSON object, and this is the console's /v1 proxy limit (nginx's default).
const maxRequestBodyBytes = 1 << 20

// registerProtectedRoutes returns the catalog capabilities are resolved over.
func registerProtectedRoutes(mux *http.ServeMux, auth *AuthMiddleware, options HandlerOptions, txManager *repository.TxManager, authorizer Authorizer) *routeCatalog {
	catalog := &routeCatalog{}
	register := protectedRouteRegistrar(mux, auth, catalog)
	var users routes.WhoamiUserRepository
	var tenants routes.WhoamiTenantRepository
	if txManager != nil {
		users = repository.NewUserRepository(txManager)
		tenants = registerDatabaseRoutes(register, options, txManager, authorizer)
	}
	// Whoami registers last so the catalog includes every route.
	routes.RegisterWhoamiRoute(register, users, tenants, resolveCapabilities(options, authorizer), catalog.sorted)
	return catalog
}

func resolveAuthorizer(options HandlerOptions) Authorizer {
	if options.Authorizer != nil {
		return options.Authorizer
	}
	if options.DB == nil {
		return nil
	}
	return repository.NewPermissionAuthorizerForDialect(options.DB, options.DBDialect)
}

// resolveCapabilities prefers the enforcing authorizer so there is one source of truth.
func resolveCapabilities(options HandlerOptions, authorizer Authorizer) routes.WhoamiCapabilityResolver {
	if options.Capabilities != nil {
		return options.Capabilities
	}
	resolver, ok := authorizer.(routes.WhoamiCapabilityResolver)
	if !ok {
		return nil
	}
	return resolver
}

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
	resolvers.defaultTo(repository.NewIdentityRepository(options.DB, options.DBDialect, options.BootstrapTenantName))
	return resolvers
}

func (r identityResolvers) complete() bool {
	return r.tenant != nil && r.user != nil && r.org != nil
}

// defaultTo uses the combined identity resolver when none are injected, because
// empty-database bootstrap must create tenant, issuer, and user atomically.
func (r *identityResolvers) defaultTo(identities *repository.IdentityRepository) {
	if r.identity == nil && r.tenant == nil && r.user == nil {
		r.identity = identities
	}
	// Consumers needing only a tenant gate on r.tenant; tenantAndUser still prefers identities.
	if r.tenant == nil {
		r.tenant = identities
	}
	if r.user == nil {
		r.user = identities
	}
	if r.org == nil {
		r.org = identities
	}
}

func newAuthMiddlewareFor(options HandlerOptions, txManager *repository.TxManager, authorizer Authorizer) (*AuthMiddleware, error) {
	resolvers := resolveIdentityResolvers(options)
	audit := options.AuditLogger
	if audit == nil && txManager != nil {
		audit = repository.NewAuditEventRepository(txManager)
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

type databaseRepositories struct {
	reviews                *repository.ReviewRepository
	reviewReviewers        *repository.ReviewReviewerRepository
	builds                 *repository.BuildRepository
	comments               *repository.CommentRepository
	tenantIssuers          *repository.TenantIssuerRepository
	tenants                *repository.TenantRepository
	environments           *repository.EnvironmentRepository
	aiSessions             *repository.AISessionRepository
	contexts               *repository.ContextRepository
	tenantQuotas           *repository.TenantQuotaRepository
	usageEvents            *repository.UsageEventRepository
	auditEvents            *repository.AuditEventRepository
	releases               *repository.ReleaseRepository
	rateLimits             *repository.PlatformRateLimitRepository
	jobs                   *repository.JobRepository
	environmentEvents      *repository.EnvironmentEventRepository
	environmentDefinitions *repository.EnvironmentDefinitionRepository
}

func newDatabaseRepositories(txManager *repository.TxManager) databaseRepositories {
	return databaseRepositories{
		reviews:                repository.NewReviewRepository(txManager),
		reviewReviewers:        repository.NewReviewReviewerRepository(txManager),
		builds:                 repository.NewBuildRepository(txManager),
		comments:               repository.NewCommentRepository(txManager),
		tenantIssuers:          repository.NewTenantIssuerRepository(txManager),
		tenants:                repository.NewTenantRepository(txManager),
		environments:           repository.NewEnvironmentRepository(txManager),
		aiSessions:             repository.NewAISessionRepository(txManager),
		contexts:               repository.NewContextRepository(txManager),
		tenantQuotas:           repository.NewTenantQuotaRepository(txManager),
		usageEvents:            repository.NewUsageEventRepository(txManager),
		auditEvents:            repository.NewAuditEventRepository(txManager),
		releases:               repository.NewReleaseRepository(txManager),
		rateLimits:             repository.NewPlatformRateLimitRepository(txManager),
		jobs:                   repository.NewJobRepository(txManager),
		environmentEvents:      repository.NewEnvironmentEventRepository(txManager),
		environmentDefinitions: repository.NewEnvironmentDefinitionRepository(txManager),
	}
}

// registerDatabaseRoutes returns the tenant repository for whoami. authorizer serves
// per-caller checks beyond the route gate (the MCP token mint's erun:admin).
func registerDatabaseRoutes(register routes.ProtectedRouteRegistrar, options HandlerOptions, txManager *repository.TxManager, authorizer Authorizer) *repository.TenantRepository {
	repos := newDatabaseRepositories(txManager)
	// Nil without a cipher, which makes deploy refuse context references rather than run
	// unauthenticated.
	var contextCredentials *repository.ContextCredentialRepository
	if options.Cipher != nil {
		contextCredentials = repository.NewContextCredentialRepository(txManager, options.Cipher)
	}
	var placementCredentials deployexec.PlacementCredentialResolver
	if contextCredentials != nil {
		placementCredentials = contextCredentials
	}
	// Running releases is the environment's job (AGENTS.md "Merge Queue"); reviewService
	// triggers the release queue on a verified MERGED transition.
	releaseService := service.NewReleaseService(repos.releases)
	releaseRoutes := routes.RegisterReleaseRoutes(register, repos.releases, releaseService)
	registerWorkflowRoutes(register, repos, releaseRoutes)
	registerEventRoutes(register, repos)
	registerTokenRoutes(register, options, repos, authorizer)
	registerCredentialRoutes(register, options, txManager, repos, contextCredentials)
	registerTenantAdminRoutes(register, options, txManager, repos)
	registerIdentityAdminRoutes(register, options, txManager)
	// Shared so provisioning and revocation use the same collaborators.
	machineIdentities := newMachineIdentityService(options, txManager, repos)
	registerMachineIdentityRoutes(register, machineIdentities)
	registerEnvironmentRoutes(register, options, repos, placementCredentials, machineIdentities)
	return repos.tenants
}

func newMachineIdentityService(options HandlerOptions, txManager *repository.TxManager, repos databaseRepositories) *service.MachineIdentityService {
	// A typed nil *zitadel.Client in the interface is not nil.
	var machineIdentityAdmin service.MachineIdentityAdmin
	if options.IdentityAdmin != nil {
		machineIdentityAdmin = options.IdentityAdmin
	}
	return service.NewMachineIdentityService(
		repos.environments,
		repository.NewUserRepository(txManager),
		repository.NewRoleRepository(txManager),
		repos.tenantIssuers,
		machineIdentityAdmin,
	)
}

// Registered unconditionally: an unconfigured provider answers an actionable 501.
func registerMachineIdentityRoutes(register routes.ProtectedRouteRegistrar, identities *service.MachineIdentityService) {
	routes.RegisterMachineIdentityRoutes(register, identities)
}

// releaseRoutes is passed in because reviewService triggers releases on MERGED.
func registerWorkflowRoutes(register routes.ProtectedRouteRegistrar, repos databaseRepositories, releaseRoutes routes.ReleaseRoutes) {
	reviewService := service.NewReviewService(repos.reviews, repos.builds, repos.comments, repos.auditEvents, gitverify.NewRemoteVerifier(), releaseRoutes)
	routes.RegisterTenantIssuerRoutes(register, repos.tenantIssuers)
	routes.RegisterReviewRoutes(register, repos.reviews, repos.reviewReviewers, reviewService)
	routes.RegisterBuildRoutes(register, repos.builds, repos.environments, service.NewBuildService(repos.builds, reviewService))
	routes.RegisterCommentRoutes(register, repos.comments, service.NewCommentService(repos.comments))
	routes.RegisterPipelineRoutes(register, service.NewPipelineService(repos.jobs, repos.reviews))
}

func registerEnvironmentRoutes(register routes.ProtectedRouteRegistrar, options HandlerOptions, repos databaseRepositories, placementCredentials deployexec.PlacementCredentialResolver, identities *service.MachineIdentityService) {
	deleter := newEnvironmentDeleter(options, repos.environments, repos.usageEvents, placementCredentials, identities)
	environmentAdmin := service.NewEnvironmentAdminService(repos.environments, repos.auditEvents)
	routes.RegisterEnvironmentRoutes(register, repos.environments, repos.tenantQuotas, repos.tenants, repos.contexts, newEnvironmentProvisioner(options, repos.environments, repos.usageEvents, placementCredentials), newEnvironmentLifecycle(options, repos.environments, repos.usageEvents, placementCredentials, identities), deleter, environmentAdmin)
	newEnvironmentDeleteReconciler(options, repos.environments, repos.tenants, repos.contexts, deleter)
	routes.RegisterAISessionRoutes(register, repos.aiSessions, repos.environments)
	routes.RegisterEnvironmentDefinitionRoutes(register, repos.environmentDefinitions, repos.environments)
	jobService := service.NewJobService(repos.jobs)
	routes.RegisterJobRoutes(register, repos.jobs, repos.environments, jobService)
	newJobAbandonReconciler(options, jobService)
}

func registerEventRoutes(register routes.ProtectedRouteRegistrar, repos databaseRepositories) {
	routes.RegisterEnvironmentEventRoutes(register, repos.environmentEvents, repos.environments)
	routes.RegisterUsageEventRoutes(register, repos.usageEvents)
	routes.RegisterAuditEventRoutes(register, repos.auditEvents)
}

func registerTokenRoutes(register routes.ProtectedRouteRegistrar, options HandlerOptions, repos databaseRepositories, authorizer Authorizer) {
	routes.RegisterMCPTokenRoutes(register, repos.environments, repos.tenants, options.MCPSigner, authorizer)
	routes.RegisterDNS01TokenRoutes(register, repos.environments, repos.tenants, options.MCPSigner)
	routes.RegisterEnvironmentHostnameRoutes(register, repos.environments, repos.tenants, options.EnvironmentHostnameWriter, options.EnvironmentHostnameServicesZone)
}

func registerCredentialRoutes(register routes.ProtectedRouteRegistrar, options HandlerOptions, txManager *repository.TxManager, repos databaseRepositories, contextCredentials *repository.ContextCredentialRepository) {
	// The alias route is always registered even without a cipher: it is the console's only
	// BYO-credential surface, and setAlias answers an actionable 501 instead of a bare 404.
	var (
		aliases            routes.CloudProviderAliasWriter
		contextProvisioner routes.ContextProvisioner
	)
	if options.Cipher != nil {
		concreteAliases := repository.NewCloudProviderAliasRepository(txManager, options.Cipher)
		aliases = concreteAliases
		if options.DBOSContext != nil {
			contextProvisioner = provision.NewProvisioner(
				options.DBOSContext,
				repos.contexts,
				contextCredentials,
				concreteAliases,
				options.Cipher,
				options.AWSEndpoint,
			)
		}
	}
	routes.RegisterCloudProviderAliasRoutes(register, aliases)
	routes.RegisterContextRoutes(register, repos.contexts, contextProvisioner)
}

func registerTenantAdminRoutes(register routes.ProtectedRouteRegistrar, options HandlerOptions, txManager *repository.TxManager, repos databaseRepositories) {
	tenantService := service.NewTenantService(repos.tenants, repos.environments, options.BootstrapTenantName)
	routes.RegisterTenantRoutes(register, repos.tenants, tenantService)
	tenantQuotaAdmin := service.NewTenantQuotaAdminService(repos.tenantQuotas, repos.auditEvents)
	routes.RegisterTenantQuotaRoute(register, tenantQuotaAdmin, repos.tenantQuotas)
	routes.RegisterConfigRoute(register, repos.tenants, repos.environments, repos.contexts, repos.rateLimits)
	routes.RegisterPlatformRateLimitRoute(register, repos.rateLimits)
	routes.RegisterProvisionRoute(register, repos.tenants, repos.environments, repos.tenantQuotas)
	routes.RegisterUserRoutes(register, repository.NewUserRepository(txManager))
	routes.RegisterRoleRoutes(register, repository.NewRoleRepository(txManager))
	// Deciding an invite request never calls Zitadel, so it needs no IdentityAdmin gate.
	inviteRequests := repository.NewInviteRequestRepository(txManager)
	inviteRequestService := service.NewInviteRequestService(inviteRequests, repos.tenants, repository.NewUserRepository(txManager), repository.NewInviteRepository(txManager))
	routes.RegisterInviteRequestRoutes(register, inviteRequests, repos.tenants, inviteRequestService)
}

func registerIdentityAdminRoutes(register routes.ProtectedRouteRegistrar, options HandlerOptions, txManager *repository.TxManager) {
	if options.IdentityAdmin == nil {
		return
	}
	userRepo := repository.NewUserRepository(txManager)
	identityService := service.NewIdentityService(options.IdentityAdmin, userRepo)
	routes.RegisterIdentityRoutes(register, options.IdentityAdmin, identityService, userRepo)
	// Without IdentityAdmin no invite could be accepted, so creation shares that gate.
	routes.RegisterInviteRoutes(register, repository.NewInviteRepository(txManager))
}

// newEnvironmentProvisioner is nil unless every precondition is met; the startup log
// names the missing ones.
func newEnvironmentProvisioner(options HandlerOptions, environments *repository.EnvironmentRepository, usage *repository.UsageEventRepository, credentials deployexec.PlacementCredentialResolver) routes.EnvironmentProvisioner {
	deploy := options.EnvDeploy
	if reasons := missingEnvProvisionerConfig(options, deploy); len(reasons) > 0 {
		log.Printf("erun api live env provisioning disabled: %s", strings.Join(reasons, "; "))
		return nil
	}
	coordinator := service.NewEnvironmentProvisioner(deployexec.NewLauncher(options.KubeClient), environments, usage, credentials)
	return provision.NewEnvProvisioner(options.DBOSContext, coordinator, deploy, newRuntimeImageChecker(options))
}

// newRuntimeImageChecker probes with the deploy Job's pull credential so a private
// registry distinguishes absent from forbidden. Nil without a KubeClient (fail open).
func newRuntimeImageChecker(options HandlerOptions) provision.RuntimeImageChecker {
	if options.KubeClient == nil {
		return nil
	}
	credentials := provision.NewKubeImagePullSecretCredentials(options.KubeClient, options.EnvDeploy.PlatformNamespace, options.EnvDeploy.ImagePullSecrets)
	return provision.NewGHCRImageChecker(credentials)
}

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

// newEnvLifecycleExecutor returns a concrete nil when unconfigured; callers must check
// before wrapping it in an interface.
func newEnvLifecycleExecutor(options HandlerOptions, environments *repository.EnvironmentRepository, usage *repository.UsageEventRepository, credentials deployexec.PlacementCredentialResolver, identities provision.MachineIdentityRevoker) *provision.EnvLifecycle {
	deploy := options.EnvDeploy
	if options.KubeClient == nil ||
		deploy.DeployerServiceAccount == "" || deploy.PlatformNamespace == "" || deploy.Registry == "" {
		return nil
	}
	return provision.NewEnvLifecycle(deployexec.NewLauncher(options.KubeClient), environments, deploy, usage, newRuntimeImageChecker(options), credentials, identities)
}

func newEnvironmentLifecycle(options HandlerOptions, environments *repository.EnvironmentRepository, usage *repository.UsageEventRepository, credentials deployexec.PlacementCredentialResolver, identities provision.MachineIdentityRevoker) routes.EnvironmentLifecycle {
	// Only Delete consults the revoker; stop and delete share collaborators.
	lifecycle := newEnvLifecycleExecutor(options, environments, usage, credentials, identities)
	if lifecycle == nil {
		return nil
	}
	return lifecycle
}

// newEnvironmentDeleter runs delete in a DBOS workflow so a restart resumes it.
func newEnvironmentDeleter(options HandlerOptions, environments *repository.EnvironmentRepository, usage *repository.UsageEventRepository, credentials deployexec.PlacementCredentialResolver, identities provision.MachineIdentityRevoker) routes.EnvironmentDeleter {
	if options.DBOSContext == nil {
		return nil
	}
	lifecycle := newEnvLifecycleExecutor(options, environments, usage, credentials, identities)
	if lifecycle == nil {
		return nil
	}
	return provision.NewEnvDeleter(options.DBOSContext, lifecycle)
}

// newEnvironmentDeleteReconciler re-attempts stuck teardowns so they converge unattended.
func newEnvironmentDeleteReconciler(options HandlerOptions, environments *repository.EnvironmentRepository, tenants *repository.TenantRepository, contexts *repository.ContextRepository, deleter routes.EnvironmentDeleter) {
	if deleter == nil {
		return
	}
	provision.NewEnvDeleteReconciler(options.DBOSContext, environments, tenants, contexts, deleter, provision.DefaultDeleteReconcileSchedule)
}

// newJobAbandonReconciler releases RUNNING jobs whose actor went away; needs DBOS.
func newJobAbandonReconciler(options HandlerOptions, jobs service.JobAbandonSweeper) {
	if options.DBOSContext == nil {
		return
	}
	service.NewJobAbandonReconciler(options.DBOSContext, jobs, service.DefaultJobAbandonTTL, service.DefaultJobAbandonSchedule)
}

func registerHealthRoute(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
}

func registerProtectedRoute(mux *http.ServeMux, auth *AuthMiddleware, method string, apiPath string, handler http.Handler) {
	// The path-id guard sits inside auth so id parsing is not observable before authentication.
	mux.Handle(method+" "+apiPath, withAPIPath(apiPath, auth.Wrap(routes.WithUUIDPathIDs(apiPath, handler))))
}

func protectedRouteRegistrar(mux *http.ServeMux, auth *AuthMiddleware, catalog *routeCatalog) routes.ProtectedRouteRegistrar {
	return func(method string, apiPath string, handler http.Handler) {
		catalog.add(method, apiPath)
		registerProtectedRoute(mux, auth, method, apiPath, handler)
	}
}
