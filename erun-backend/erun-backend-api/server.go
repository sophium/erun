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
	// Capabilities answers GET /v1/whoami's capability set. Unset defaults to
	// the configured Authorizer when it can resolve one — the database-backed
	// authorizer does — so the capability answer and enforcement come from one
	// place. An Authorizer that cannot leaves whoami without a capability set
	// rather than claiming an empty one.
	Capabilities routes.WhoamiCapabilityResolver
	DB           *sql.DB
	DBDialect    repository.Dialect
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
	// EnvironmentHostnameWriter backs PUT/DELETE
	// /v1/environments/{environment_id}/hostname: the same
	// PowerDNS write path DNS01Broker uses, reused so a tenant with no direct
	// PowerDNS access to the platform cluster can still point its own
	// environment's wildcard hostname at an IP. Nil when unconfigured; the
	// route then reports 501 rather than claiming a write it cannot perform.
	EnvironmentHostnameWriter routes.EnvironmentHostnameWriter
	// EnvironmentHostnameServicesZone is the zone the hostname route resolves
	// a caller's environment name into, matching DNS01Broker's own zone.
	EnvironmentHostnameServicesZone string
	// KubeClient runs the server-side env-deploy Jobs. Nil (the default outside a
	// cluster) leaves env provisioning off: POST /v1/environments only registers
	// the row. Set together with EnvDeploy and DBOSContext to enable live deploys.
	KubeClient kubernetes.Interface
	// EnvDeploy is the per-instance placement for env-deploy Jobs (image registry,
	// platform namespace, cluster-admin deployer ServiceAccount). Env provisioning
	// stays off until all three are set.
	EnvDeploy provision.EnvDeployConfig
	// Platform is this instance's own self-describing config, served
	// unauthenticated at GET /v1/platform so a client can discover it before it
	// has a token. Unset fields render as empty strings, never as an error.
	Platform routes.PlatformInfo
	// BootstrapTenantName is this instance's own declared tenant identity
	// (ERUN_TENANT), used to name the tenant empty-database bootstrap enrols
	// instead of a generic placeholder. Empty falls back to that placeholder.
	BootstrapTenantName string
	// IdentityAdmin drives Zitadel's Management API for identity
	// administration (issue #1209): the org-owner PAT the erun-zitadel chart
	// already provisions, read from a mounted file. Nil when unconfigured —
	// the /v1/identity/* routes are then not registered at all, rather than
	// present and always failing.
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
	// The hosted registry's token service authenticates its own HTTP Basic
	// credential (the tenant's erun-api bearer token as the password, not a
	// standard Authorization: Bearer header), so like the DNS-01 broker it is
	// registered directly on the mux, outside registerProtectedRoutes' catalog.
	// It reuses the MCP signing key (distinct audience per mint, same key — see
	// mcptoken.Signer.SignRegistry) and the same tenant resolver the primary
	// auth path uses, so a registry token can only ever be minted for the
	// tenant the caller's own token resolves to.
	if resolvers := resolveIdentityResolvers(options); options.MCPSigner != nil && options.TokenVerifier != nil && resolvers.tenant != nil {
		registrytoken.NewHandler(options.TokenVerifier, resolvers.tenant, options.MCPSigner).Register(mux)
	}
	// Accepting an invite (#1483) has no bearer token to authenticate with —
	// the token in the request body is the credential — so it is registered
	// directly on the mux like the platform route above. It needs the same
	// Zitadel client identity administration does, so it shares that
	// optional-dependency gate: unconfigured leaves the route unregistered
	// entirely rather than present and always failing.
	if txManager != nil && options.IdentityAdmin != nil {
		inviteService := service.NewInviteService(repository.NewInviteRepository(txManager), options.IdentityAdmin, repository.NewUserRepository(txManager))
		routes.RegisterInviteAcceptRoute(mux, inviteService)
	}
	// Requesting an invitation also has no bearer token resolvable to a
	// tenant — the requester is authenticated at their own IdP but enrolled
	// nowhere, which the normal protected registrar's tenant/user resolution
	// would reject — so like invite acceptance it registers directly on the
	// mux. Unlike accept, it does verify a real bearer token (just not to a
	// tenant/user), so it only needs the token verifier and a database, not
	// Zitadel identity admin.
	if txManager != nil {
		routes.RegisterInviteRequestPublicRoutes(
			mux,
			options.TokenVerifier,
			repository.NewInviteRequestRepository(txManager),
			routes.NewInviteRequestRateLimiter(repository.NewPlatformRateLimitRepository(txManager)),
		)
	}
	registerProtectedRoutes(mux, auth, options, txManager, authorizer)
	return mux, nil
}

// registerProtectedRoutes registers every authenticated route and returns the
// catalog of their canonical (method, path) pairs — the candidate set the
// capability answer is resolved over.
func registerProtectedRoutes(mux *http.ServeMux, auth *AuthMiddleware, options HandlerOptions, txManager *repository.TxManager, authorizer Authorizer) *routeCatalog {
	catalog := &routeCatalog{}
	register := protectedRouteRegistrar(mux, auth, catalog)
	var users routes.WhoamiUserRepository
	if txManager != nil {
		users = repository.NewUserRepository(txManager)
		registerDatabaseRoutes(register, options, txManager, authorizer)
	}
	// Whoami registers last, and reads the catalog per request, so its own route
	// and every route above it are in the capability answer.
	routes.RegisterWhoamiRoute(register, users, resolveCapabilities(options, authorizer), catalog.sorted)
	return catalog
}

// resolveAuthorizer reports the endpoint authorizer, defaulting to the
// database-backed one.
func resolveAuthorizer(options HandlerOptions) Authorizer {
	if options.Authorizer != nil {
		return options.Authorizer
	}
	if options.DB == nil {
		return nil
	}
	return repository.NewPermissionAuthorizerForDialect(options.DB, options.DBDialect)
}

// resolveCapabilities reports what answers whoami's capability set. It prefers
// the authorizer that enforces access, because a second implementation would
// be a second place for the answer to be wrong.
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
	resolvers.defaultTo(repository.NewIdentityRepository(options.DB, options.DBDialect, options.BootstrapTenantName))
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
	}
	// Fill the plain tenant resolver even when the combined identity resolver
	// above is the one auth will use. tenantAndUser prefers identities whenever
	// it is set, so this cannot move the atomic-bootstrap path — but a consumer
	// that needs only a tenant is gated on this field, and leaving it nil is why
	// the hosted registry token service never registered in any deployment that
	// injects no resolvers of its own.
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

// newAuthMiddlewareFor assembles the authentication middleware, defaulting every
// database-backed dependency the caller did not inject.
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

// databaseRepositories bundles every repository registerDatabaseRoutes wires,
// so registration reads as what each route needs rather than as 12 repeated
// repository.NewXRepository(txManager) calls (#1302).
type databaseRepositories struct {
	reviews         *repository.ReviewRepository
	reviewReviewers *repository.ReviewReviewerRepository
	builds          *repository.BuildRepository
	comments        *repository.CommentRepository
	tenantIssuers   *repository.TenantIssuerRepository
	tenants         *repository.TenantRepository
	environments    *repository.EnvironmentRepository
	aiSessions      *repository.AISessionRepository
	contexts        *repository.ContextRepository
	tenantQuotas    *repository.TenantQuotaRepository
	usageEvents     *repository.UsageEventRepository
	auditEvents     *repository.AuditEventRepository
	releases        *repository.ReleaseRepository
	rateLimits      *repository.PlatformRateLimitRepository
	gateRuns        *repository.GateRunRepository
}

func newDatabaseRepositories(txManager *repository.TxManager) databaseRepositories {
	return databaseRepositories{
		reviews:         repository.NewReviewRepository(txManager),
		reviewReviewers: repository.NewReviewReviewerRepository(txManager),
		builds:          repository.NewBuildRepository(txManager),
		comments:        repository.NewCommentRepository(txManager),
		tenantIssuers:   repository.NewTenantIssuerRepository(txManager),
		tenants:         repository.NewTenantRepository(txManager),
		environments:    repository.NewEnvironmentRepository(txManager),
		aiSessions:      repository.NewAISessionRepository(txManager),
		contexts:        repository.NewContextRepository(txManager),
		tenantQuotas:    repository.NewTenantQuotaRepository(txManager),
		usageEvents:     repository.NewUsageEventRepository(txManager),
		auditEvents:     repository.NewAuditEventRepository(txManager),
		releases:        repository.NewReleaseRepository(txManager),
		rateLimits:      repository.NewPlatformRateLimitRepository(txManager),
		gateRuns:        repository.NewGateRunRepository(txManager),
	}
}

// registerDatabaseRoutes registers every route backed by persistence, which is
// all of them except the health check and the DNS-01 broker. authorizer is
// threaded through only for the routes that need a per-caller entitlement
// check beyond the route-level TenantUser/TenantAdmin gate already enforced
// by the outer middleware -- today just the MCP token mint route's erun:admin
// check (erun#1891).
func registerDatabaseRoutes(register routes.ProtectedRouteRegistrar, options HandlerOptions, txManager *repository.TxManager, authorizer Authorizer) {
	repos := newDatabaseRepositories(txManager)
	// contextCredentials resolves a placed environment's live admin token
	// (#1112). nil without a cipher (the same precondition context
	// bootstrapping itself already requires), which leaves every context
	// reference refused at deploy time rather than silently unauthenticated —
	// see deployexec.ResolvePlacementToken.
	var contextCredentials *repository.ContextCredentialRepository
	if options.Cipher != nil {
		contextCredentials = repository.NewContextCredentialRepository(txManager, options.Cipher)
	}
	var placementCredentials deployexec.PlacementCredentialResolver
	if contextCredentials != nil {
		placementCredentials = contextCredentials
	}
	// releaseService owns only the release queue's idempotency: recording a
	// trigger exactly once per (tenant, commit). Running `erun release` is the
	// environment's own job, not this control plane's — see AGENTS.md "Merge
	// Queue" — so releaseRoutes has to exist before reviewService, which
	// triggers it directly on a verified MERGED transition.
	releaseService := service.NewReleaseService(repos.releases)
	releaseRoutes := routes.RegisterReleaseRoutes(register, repos.releases, releaseService)
	reviewService := service.NewReviewService(repos.reviews, repos.builds, repos.comments, repos.auditEvents, gitverify.NewRemoteVerifier(), releaseRoutes)
	commentService := service.NewCommentService(repos.comments)
	buildService := service.NewBuildService(repos.builds, reviewService)
	routes.RegisterTenantIssuerRoutes(register, repos.tenantIssuers)
	routes.RegisterReviewRoutes(register, repos.reviews, repos.reviewReviewers, reviewService)
	routes.RegisterBuildRoutes(register, repos.builds, buildService)
	routes.RegisterCommentRoutes(register, repos.comments, commentService)
	routes.RegisterGateRunRoutes(register, repos.gateRuns, service.NewGateRunService(repos.gateRuns))
	deleter := newEnvironmentDeleter(options, repos.environments, repos.usageEvents, placementCredentials)
	environmentAdmin := service.NewEnvironmentAdminService(repos.environments, repos.auditEvents)
	routes.RegisterEnvironmentRoutes(register, repos.environments, repos.tenantQuotas, repos.tenants, repos.contexts, newEnvironmentProvisioner(options, repos.environments, repos.usageEvents, placementCredentials), newEnvironmentLifecycle(options, repos.environments, repos.usageEvents, placementCredentials), deleter, environmentAdmin)
	newEnvironmentDeleteReconciler(options, repos.environments, repos.tenants, repos.contexts, deleter)
	routes.RegisterAISessionRoutes(register, repos.aiSessions, repos.environments)
	routes.RegisterUsageEventRoutes(register, repos.usageEvents)
	routes.RegisterAuditEventRoutes(register, repos.auditEvents)
	routes.RegisterMCPTokenRoutes(register, repos.environments, repos.tenants, options.MCPSigner, authorizer)
	routes.RegisterDNS01TokenRoutes(register, repos.environments, repos.tenants, options.MCPSigner)
	routes.RegisterEnvironmentHostnameRoutes(register, repos.environments, repos.tenants, options.EnvironmentHostnameWriter, options.EnvironmentHostnameServicesZone)
	// aliases is nil without a cipher, the same precondition every other
	// Cipher-gated dependency on this page requires -- but unlike those (which
	// simply leave a caller with a narrower feature set), the console's own
	// ProvisionPanel is the *only* operator surface for registering BYO-cloud
	// credentials, so a route left unregistered here 404s with no diagnosis
	// at all: "alias request failed (404)" gives an operator nothing to act
	// on, exactly the "advice that cannot work" dead end root AGENTS.md's
	// "Smooth, Seamless, No Dead Ends" section calls a defect. The route is
	// therefore always registered; setAlias itself reports the missing
	// configuration with a named, actionable 501 (the same pattern
	// mintMCPToken uses for a nil signer) instead of the mux's bare 404.
	var aliases routes.CloudProviderAliasWriter
	var contextProvisioner routes.ContextProvisioner
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
	tenantService := service.NewTenantService(repos.tenants, repos.environments, options.BootstrapTenantName)
	routes.RegisterTenantRoutes(register, repos.tenants, tenantService)
	tenantQuotaAdmin := service.NewTenantQuotaAdminService(repos.tenantQuotas, repos.auditEvents)
	routes.RegisterTenantQuotaRoute(register, tenantQuotaAdmin, repos.tenantQuotas)
	routes.RegisterConfigRoute(register, repos.tenants, repos.environments, repos.contexts, repos.rateLimits)
	routes.RegisterPlatformRateLimitRoute(register, repos.rateLimits)
	routes.RegisterProvisionRoute(register, repos.tenants, repos.environments, repos.tenantQuotas)
	routes.RegisterUserRoutes(register, repository.NewUserRepository(txManager))
	routes.RegisterRoleRoutes(register, repository.NewRoleRepository(txManager))
	// Deciding an invite request needs no Zitadel identity administration —
	// unlike accepting an invite, the requester already has a usable external
	// identity, so approval only enrols it (UserRepository.Create) and mints
	// an invite (InviteRepository.Create), neither of which calls Zitadel —
	// so this registers unconditionally rather than sharing
	// registerIdentityAdminRoutes' IdentityAdmin gate below.
	inviteRequests := repository.NewInviteRequestRepository(txManager)
	inviteRequestService := service.NewInviteRequestService(inviteRequests, repos.tenants, repository.NewUserRepository(txManager), repository.NewInviteRepository(txManager))
	routes.RegisterInviteRequestRoutes(register, inviteRequests, repos.tenants, inviteRequestService)
	registerIdentityAdminRoutes(register, options, txManager)
}

// registerIdentityAdminRoutes wires /v1/identity/* (issue #1209) when a
// Zitadel Management API client is configured; nil leaves the routes
// unregistered entirely, matching every other optional dependency's
// convention in this file.
func registerIdentityAdminRoutes(register routes.ProtectedRouteRegistrar, options HandlerOptions, txManager *repository.TxManager) {
	if options.IdentityAdmin == nil {
		return
	}
	userRepo := repository.NewUserRepository(txManager)
	identityService := service.NewIdentityService(options.IdentityAdmin, userRepo)
	routes.RegisterIdentityRoutes(register, options.IdentityAdmin, identityService, userRepo)
	// Invite creation (#1483) is only meaningful when this platform enrolls
	// users through the shared Zitadel org identity administration already
	// depends on — a platform with no IdentityAdmin has no accept path an
	// invite could ever complete, so it shares that same optional-dependency
	// gate rather than registering a dead end.
	routes.RegisterInviteRoutes(register, repository.NewInviteRepository(txManager))
}

// newEnvironmentProvisioner wires live env provisioning, which needs durable
// workflows, an in-cluster client, and the full deploy placement. Anything
// missing leaves it nil, so env creation only registers the row. Without a
// startup log naming which precondition failed, an operator sees only a 501
// at call time with no way to tell which of the five is unmet.
func newEnvironmentProvisioner(options HandlerOptions, environments *repository.EnvironmentRepository, usage *repository.UsageEventRepository, credentials deployexec.PlacementCredentialResolver) routes.EnvironmentProvisioner {
	deploy := options.EnvDeploy
	if reasons := missingEnvProvisionerConfig(options, deploy); len(reasons) > 0 {
		log.Printf("erun api live env provisioning disabled: %s", strings.Join(reasons, "; "))
		return nil
	}
	coordinator := service.NewEnvironmentProvisioner(deployexec.NewLauncher(options.KubeClient), environments, usage, credentials)
	return provision.NewEnvProvisioner(options.DBOSContext, coordinator, deploy, newRuntimeImageChecker(options))
}

// newRuntimeImageChecker wires the one published-image probe every Job that
// runs a tenant's erun toolchain shares (deploy, stop, delete, the
// release-queue runner) — each used to make its own image decision, and only
// deploy fell back. It runs with the deploy Job's own pull credential, read
// from the platform namespace, so a private registry namespace answers it
// decisively instead of identically for absent and forbidden. Nil without an
// in-cluster client to read the credential Secret with, which leaves every
// caller on its fail-open default (the tenant's own image, unconditionally).
func newRuntimeImageChecker(options HandlerOptions) provision.RuntimeImageChecker {
	if options.KubeClient == nil {
		return nil
	}
	credentials := provision.NewKubeImagePullSecretCredentials(options.KubeClient, options.EnvDeploy.PlatformNamespace, options.EnvDeploy.ImagePullSecrets)
	return provision.NewGHCRImageChecker(credentials)
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

// newEnvLifecycleExecutor builds the shared stop/delete executor both
// newEnvironmentLifecycle (stop) and newEnvironmentDeleter (delete) wrap, so
// the two never drift onto different image or credential resolution. Needs an
// in-cluster client and the full deploy placement; nil (a concrete, not yet
// interface-wrapped nil — callers must check before returning it as an
// interface) when either is missing.
func newEnvLifecycleExecutor(options HandlerOptions, environments *repository.EnvironmentRepository, usage *repository.UsageEventRepository, credentials deployexec.PlacementCredentialResolver) *provision.EnvLifecycle {
	deploy := options.EnvDeploy
	if options.KubeClient == nil ||
		deploy.DeployerServiceAccount == "" || deploy.PlatformNamespace == "" || deploy.Registry == "" {
		return nil
	}
	return provision.NewEnvLifecycle(deployexec.NewLauncher(options.KubeClient), environments, deploy, usage, newRuntimeImageChecker(options), credentials)
}

// newEnvironmentLifecycle wires live stop, which needs an in-cluster client
// and the full deploy placement but no durable workflow. Anything missing
// leaves it nil, so stop reports the executor as unconfigured rather than
// acting on partial config.
func newEnvironmentLifecycle(options HandlerOptions, environments *repository.EnvironmentRepository, usage *repository.UsageEventRepository, credentials deployexec.PlacementCredentialResolver) routes.EnvironmentLifecycle {
	lifecycle := newEnvLifecycleExecutor(options, environments, usage, credentials)
	if lifecycle == nil {
		return nil
	}
	return lifecycle
}

// newEnvironmentDeleter wires live delete (#1140): the same stop/delete
// executor newEnvironmentLifecycle wraps, but run inside a durable DBOS
// workflow so a control-plane restart resumes an in-flight delete rather than
// leaving the environment stranded in `deleting`. nil under the same
// preconditions as newEnvironmentLifecycle, plus a configured DBOSContext —
// without one there is nowhere durable to run the workflow.
func newEnvironmentDeleter(options HandlerOptions, environments *repository.EnvironmentRepository, usage *repository.UsageEventRepository, credentials deployexec.PlacementCredentialResolver) routes.EnvironmentDeleter {
	if options.DBOSContext == nil {
		return nil
	}
	lifecycle := newEnvLifecycleExecutor(options, environments, usage, credentials)
	if lifecycle == nil {
		return nil
	}
	return provision.NewEnvDeleter(options.DBOSContext, lifecycle)
}

// newEnvironmentDeleteReconciler schedules the periodic re-attempt of every
// environment mid-teardown, so a namespace that finishes terminating
// — or a solver that starts answering again — converges the row without an
// operator noticing and re-issuing the delete. Registered only when delete
// itself is wired: with no live executor there is nothing for a reconciled
// attempt to run.
func newEnvironmentDeleteReconciler(options HandlerOptions, environments *repository.EnvironmentRepository, tenants *repository.TenantRepository, contexts *repository.ContextRepository, deleter routes.EnvironmentDeleter) {
	if deleter == nil {
		return
	}
	provision.NewEnvDeleteReconciler(options.DBOSContext, environments, tenants, contexts, deleter, provision.DefaultDeleteReconcileSchedule)
}

func registerHealthRoute(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
}

func registerProtectedRoute(mux *http.ServeMux, auth *AuthMiddleware, method string, apiPath string, handler http.Handler) {
	mux.Handle(method+" "+apiPath, withAPIPath(apiPath, auth.Wrap(handler)))
}

func protectedRouteRegistrar(mux *http.ServeMux, auth *AuthMiddleware, catalog *routeCatalog) routes.ProtectedRouteRegistrar {
	return func(method string, apiPath string, handler http.Handler) {
		catalog.add(method, apiPath)
		registerProtectedRoute(mux, auth, method, apiPath, handler)
	}
}
