// Package routeroles is the single source of truth for which of the narrower
// predefined roles (TenantUser, TenantAdmin, TenantAgent) grant which
// registered API route. It exists in its own leaf package, rather than inside
// internal/routes where the route registration calls actually live, because
// internal/repository needs to import it to seed role_permissions rows and
// repository must not import routes (see erun-backend-api/AGENTS.md's Layer
// Layout: repositories may import model, not routes) — the same reason
// internal/security lives outside all four layers instead of inside one of
// them.
//
// Every registered protected route must appear in Routes exactly once.
// erun-integration's role-classification gate (the same "classify every
// route or fail" treatment as its desktop-surface gate) parses both this
// file and internal/routes' registration call sites and fails when a
// registered route has no entry here — so a route added in the future forces
// a deliberate decision about who may call it, rather than silently landing
// inside or outside a role. See that gate's own doc for the reasoning this
// classification exists to enforce: a role defined by enumerated exact paths
// silently fails to grant a route nobody remembered to add, and TenantUser
// and TenantAdmin are built from exactly this map's exact-path entries so
// there is no second, hand-authored regex list that could drift from it.
package routeroles

// Roles is the set of narrower predefined roles that grant one registered
// route. It is a membership set rather than a single class per route because
// TenantAgent is a strict subset of TenantUser's reach: a route can be
// granted to TenantUser and TenantAdmin without being granted to the machine
// role, and no single-valued constant can express that without either
// over-granting the machine role or narrowing TenantUser for every tenant
// that already holds it. A bit set says exactly which roles a route is for,
// and each derived role's grant list is one mask over it.
type Roles uint8

const (
	// TenantUserRole is the bit TenantUser's derived grant set is built
	// from: reading the tenant, and driving reviews/comments/builds/the
	// merge queue and existing environments.
	TenantUserRole Roles = 1 << iota
	// TenantAdminRole is the bit TenantAdmin's derived grant set is built
	// from: full tenant administration (environments, contexts, users,
	// invites, roles), never operations.
	TenantAdminRole
	// TenantAgentRole is the bit TenantAgent's derived grant set is built
	// from — the machine role an environment's own identity holds. It is a
	// deliberate strict subset of TenantUser's reach: an unattended
	// environment drives the merge-queue gate and reports its own build, and
	// nothing else. Extend it only after identifying a real additional
	// caller, the same discipline the whole narrower-role model exists for.
	TenantAgentRole
)

const (
	// OperationsOnly means no narrower predefined role grants this route. It
	// stays reachable only through the wildcard ReadAll/WriteAll roles — a
	// platform operator — even from inside an OPERATIONS tenant. Every route
	// classified here is already gated at the handler by tenant type (or, for
	// GET/PATCH /v1/tenant-issuers, shares a route with a half that is), so
	// this classification is the role-permission layer's half of that same
	// defense in depth.
	OperationsOnly Roles = 0
	// TenantUserClass means TenantUser and TenantAdmin both grant this
	// route, and TenantAgent does not.
	TenantUserClass Roles = TenantUserRole | TenantAdminRole
	// TenantAdminOnly means TenantAdmin grants this route and TenantUser
	// does not.
	TenantAdminOnly Roles = TenantAdminRole
	// TenantAgentClass means TenantUser, TenantAdmin and TenantAgent all
	// grant this route — a route that is part of driving the merge-queue
	// gate or reporting the environment's own build. Every route classified
	// this way is a subset member, never a route of its own.
	TenantAgentClass Roles = TenantUserRole | TenantAdminRole | TenantAgentRole
)

// Routes classifies every registered protected API route by the set of
// narrower predefined roles that grant it, keyed "<METHOD> <canonical path
// template>" exactly as passed to ProtectedRouteRegistrar — the same key
// shape route_audit.go's InternalAPIRoutes/KnownUnsurfacedRoutes use.
// Grouped by the route file each entry comes from so a reviewer can diff
// this against internal/routes/*.go directly.
//
// TenantAgentClass appears on exactly the routes an environment's own
// identity performs while it drives the merge-queue gate and reports its own
// build: reading its own capabilities and the reviews it is gated against,
// reporting the GATE build, starting and reporting the gate run, reporting
// the merge, and the unattached `erun build` self-report. Two boundaries are
// deliberate rather than omissions. GET /v1/builds is not among them: it is
// the tenant-wide build history, read by the desktop dashboard and the
// console, and no environment flow reads it — the self-report needs only the
// POST half, and widening the machine role to a tenant-wide history read
// would be a grant with no caller behind it. And the environment's own
// build self-report is POST /v1/builds rather than only the review-nested
// report: that unattached route is what `erun build` actually calls, so a
// machine role scoped to the nested report alone would 403 on the call the
// environment makes today.
var Routes = map[string]Roles{
	// whoami.go — every authenticated caller needs this to learn their own
	// capabilities, regardless of role.
	"GET /v1/whoami": TenantAgentClass,

	// config.go — the console read model, RLS-scoped to the caller's tenant.
	"GET /v1/config": TenantUserClass,

	// tenants.go — list/reachable are tenant self-service (RLS/handler logic
	// limits a non-operations caller to their own tenant already); creating a
	// tenant and the one-time bootstrap-name repair are gated to an
	// OPERATIONS tenant at the handler.
	"GET /v1/tenants":                            TenantUserClass,
	"GET /v1/tenants/reachable":                  TenantUserClass,
	"POST /v1/tenants":                           OperationsOnly,
	"PATCH /v1/tenants/reconcile-bootstrap-name": OperationsOnly,

	// tenant_quotas.go — GET /v1/quota is tenant self-service; the write is
	// explicitly operations-only per its own registration comment.
	"GET /v1/quota":                     TenantUserClass,
	"PUT /v1/tenants/{tenant_id}/quota": OperationsOnly,

	// tenant_issuers.go — PATCH shares one route between a tenant-self-service
	// rename and an operations-only org-scope conversion (the handler itself
	// gates the org-scope half). Classified operations-only as a whole: it has
	// no console/desktop surface yet (route_audit.go's KnownUnsurfacedRoutes),
	// and the route's own doc comment already treats it as belonging to the
	// same root-resolution-table family POST /v1/tenants does.
	"GET /v1/tenant-issuers":   OperationsOnly,
	"PATCH /v1/tenant-issuers": OperationsOnly,

	// usage_events.go, audit_events.go — read-only, RLS-scoped tenant reads.
	"GET /v1/usage-events": TenantUserClass,
	"GET /v1/audit-events": TenantUserClass,

	// reviews.go — driving reviews and the merge queue is exactly what
	// TenantUser is for. The reading half and the status report are what an
	// environment's own identity does; opening a review, promoting the
	// queue, assigning reviewers and closing one are the operator's own
	// acts, so TenantAgent is not granted them.
	"GET /v1/reviews":                                    TenantAgentClass,
	"POST /v1/reviews":                                   TenantUserClass,
	"GET /v1/reviews/merge-queue":                        TenantAgentClass,
	"POST /v1/reviews/merge-queue/advance":               TenantUserClass,
	"POST /v1/reviews/merge-queue/override-advance":      TenantUserClass,
	"GET /v1/reviews/{review_id}":                        TenantAgentClass,
	"PATCH /v1/reviews/{review_id}/status":               TenantAgentClass,
	"GET /v1/reviews/{review_id}/reviewers":              TenantUserClass,
	"POST /v1/reviews/{review_id}/reviewers":             TenantUserClass,
	"DELETE /v1/reviews/{review_id}/reviewers/{user_id}": TenantUserClass,

	// builds.go — reporting/reading a review's builds is part of driving it.
	// GET/POST /v1/builds is the same build-history surface, tenant-wide
	// rather than review-nested (erun#1954). A review's own builds are part
	// of reading that review — `erun review show` fetches them alongside its
	// comments — and reporting the GATE build is the whole reason the
	// machine role exists. The tenant-wide list is not: see Routes' own doc
	// comment for why TenantAgent gets the POST half of /v1/builds without
	// the GET half.
	"GET /v1/reviews/{review_id}/builds":            TenantAgentClass,
	"POST /v1/reviews/{review_id}/builds":           TenantAgentClass,
	"GET /v1/reviews/{review_id}/builds/{build_id}": TenantUserClass,
	"GET /v1/builds":                                TenantUserClass,
	"POST /v1/builds":                               TenantAgentClass,

	// gate_runs.go — reading and driving gate runs is part of the same
	// merge-queue workflow TenantUser already drives for reviews and builds.
	// The environment driving the gate reports its own run, so the create
	// and update pair is the machine role's; the reads are the operator's
	// view of the queue's history.
	"GET /v1/gate-runs":                 TenantUserClass,
	"POST /v1/gate-runs":                TenantAgentClass,
	"GET /v1/gate-runs/{gate_run_id}":   TenantUserClass,
	"PATCH /v1/gate-runs/{gate_run_id}": TenantAgentClass,

	// environment_events.go — reading an environment's event log and
	// appending to it is reading and operating environments that already
	// exist, the same reach TenantUser already has over ai_sessions.
	"GET /v1/events": TenantUserClass,
	"POST /v1/environments/{environment_id}/events": TenantUserClass,

	// comments.go — driving review comments. Reading them is part of reading
	// the review (`erun review show` fetches comments unconditionally);
	// writing one, or resolving a thread, is an operator's act.
	"GET /v1/reviews/{review_id}/comments":                       TenantAgentClass,
	"POST /v1/reviews/{review_id}/comments":                      TenantUserClass,
	"PATCH /v1/reviews/{review_id}/comments/{comment_id}/status": TenantUserClass,

	// releases.go — reading releases and triggering one for a merged commit
	// are downstream of the same merge-queue workflow TenantUser drives.
	"GET /v1/releases":                     TenantUserClass,
	"POST /v1/releases":                    TenantUserClass,
	"GET /v1/releases/{release_id}":        TenantUserClass,
	"GET /v1/reviews/{review_id}/releases": TenantUserClass,

	// environments.go — operating an environment that already exists (read,
	// deploy, stop) is TenantUser; creating or deleting one is tenant
	// administration.
	"GET /v1/environments":                          TenantUserClass,
	"POST /v1/environments":                         TenantAdminOnly,
	"GET /v1/environments/{environment_id}":         TenantUserClass,
	"POST /v1/environments/{environment_id}/deploy": TenantUserClass,
	"POST /v1/environments/{environment_id}/stop":   TenantUserClass,
	"DELETE /v1/environments/{environment_id}":      TenantAdminOnly,

	// dns01_token.go, mcp_token.go — minting a token scoped to an environment
	// that already exists is operating it, not administering the tenant.
	"POST /v1/environments/{environment_id}/dns01-token": TenantUserClass,
	"POST /v1/environments/{environment_id}/mcp-token":   TenantUserClass,

	// environment_hostname.go — pointing an already-existing environment's
	// own wildcard hostname at an IP is the same class as the
	// dns01-token mint above: operating an environment that already exists,
	// never administering the tenant, and never any other tenant's
	// environment (the route resolves the wildcard name from the caller's
	// own environment row, never from caller-supplied input).
	"PUT /v1/environments/{environment_id}/hostname":    TenantUserClass,
	"DELETE /v1/environments/{environment_id}/hostname": TenantUserClass,

	// ai_sessions.go — an environment self-reporting its own AI-session status
	// is the same class as reporting a build result: operating an environment
	// that already exists, not administering the tenant. Reading it back is
	// the same class as reading the environment itself.
	"POST /v1/environments/{environment_id}/ai-sessions": TenantUserClass,
	"GET /v1/environments/{environment_id}/ai-sessions":  TenantUserClass,

	// environment_definitions.go — uploading the portable subset of an
	// environment's own settings, and reading it back, is the same class as
	// the ai-sessions self-report above: operating an environment that already
	// exists. It creates nothing and deletes nothing — the environment row is
	// registered (or adopted) through POST /v1/environments, which stays
	// tenant administration.
	"PUT /v1/environments/{environment_id}/definition": TenantUserClass,
	"GET /v1/environments/{environment_id}/definition": TenantUserClass,

	// jobs.go — recording and updating what this caller is working on is the
	// same class as reporting a build result: an actor's own account of its
	// work, not administration of the tenant. Reading the queue back is the
	// same class as reading the tenant's builds.
	"GET /v1/jobs":                               TenantUserClass,
	"POST /v1/jobs":                              TenantUserClass,
	"GET /v1/jobs/{job_id}":                      TenantUserClass,
	"PATCH /v1/jobs/{job_id}":                    TenantUserClass,
	"GET /v1/environments/{environment_id}/jobs": TenantUserClass,

	// pipeline.go — reading the pipeline is reading the tenant's own jobs and
	// reviews together, one read of what the two routes above and the review
	// listings already expose separately. It grants reach over no data
	// TenantUser cannot already read, and it writes nothing at all.
	"GET /v1/pipeline": TenantUserClass,

	// contexts.go — reading registered contexts is TenantUser; registering a
	// new one is tenant administration (explicitly named in the issue this
	// classification implements).
	"GET /v1/contexts":              TenantUserClass,
	"POST /v1/contexts":             TenantAdminOnly,
	"GET /v1/contexts/{context_id}": TenantUserClass,

	// cloud_provider_aliases.go — storing BYO-cloud credentials feeds context
	// registration, so it carries the same tenant-administration weight.
	"PUT /v1/cloud-provider-aliases/{alias}": TenantAdminOnly,

	// provision.go — the preview for creating an environment/context is part
	// of that same administrative action, even though it writes nothing.
	"POST /v1/provision": TenantAdminOnly,

	// roles.go — reading roles and a user's own role assignments is
	// TenantUser; creating a role and granting/revoking one is administering
	// the tenant's authorization model.
	"GET /v1/roles":                              TenantUserClass,
	"POST /v1/roles":                             TenantAdminOnly,
	"GET /v1/users/{user_id}/roles":              TenantUserClass,
	"POST /v1/users/{user_id}/roles":             TenantAdminOnly,
	"DELETE /v1/users/{user_id}/roles/{role_id}": TenantAdminOnly,

	// users.go — reading enrolled users is TenantUser; enrolling one is
	// tenant administration.
	"GET /v1/users":  TenantUserClass,
	"POST /v1/users": TenantAdminOnly,

	// identity.go — administering the platform's own shared IdP is
	// restricted to an OPERATIONS tenant at every handler already; TenantAdmin
	// grants none of it, so it stays a genuinely lesser position than the
	// operator role even inside the OPERATIONS tenant.
	"GET /v1/identity/users":                           OperationsOnly,
	"POST /v1/identity/users":                          OperationsOnly,
	"POST /v1/identity/users/{external_id}/deactivate": OperationsOnly,
	"POST /v1/identity/users/{external_id}/reactivate": OperationsOnly,
	"POST /v1/identity/orgs":                           OperationsOnly,
	"GET /v1/identity/org-settings":                    OperationsOnly,
	"PATCH /v1/identity/org-settings":                  OperationsOnly,
	"GET /v1/identity/smtp-settings":                   OperationsOnly,
	"PATCH /v1/identity/smtp-settings":                 OperationsOnly,

	// invites.go — reading pending invites is TenantUser; minting or revoking
	// one is tenant administration (invites.go's own cross-tenant case is
	// handled by resolveTargetTenant's handler-level check, same as users.go).
	"POST /v1/invites":               TenantAdminOnly,
	"GET /v1/invites":                TenantUserClass,
	"DELETE /v1/invites/{invite_id}": TenantAdminOnly,

	// invite_requests.go — reading the queue is TenantUser (a non-operations
	// caller only ever sees JOIN_TENANT requests naming their own tenant, per
	// the handler's own filter); deciding one is tenant administration for a
	// JOIN_TENANT request (the handler additionally requires an operations
	// tenant for CREATE_TENANT, the same shared-route-shared-permission shape
	// tenant_issuers.go's PATCH already uses). POST /v1/invite-requests and
	// GET /v1/invite-requests/mine are registered unauthenticated, directly
	// on the mux (see server.go), so they never reach PermissionAuthorizer
	// and are intentionally absent here — same as invites.go's accept route.
	"GET /v1/invite-requests":                              TenantUserClass,
	"POST /v1/invite-requests/{invite_request_id}/approve": TenantAdminOnly,
	"POST /v1/invite-requests/{invite_request_id}/decline": TenantAdminOnly,

	// platform_rate_limits.go — changing the invite-request rate-limit window
	// is restricted to an operations tenant at the handler, the same
	// tenant_quotas.go PUT shape.
	"PATCH /v1/config/invite-request-rate-limit": OperationsOnly,
}

// RoutePermission is one exact (method, path) grant, matching
// role_permissions' exact-pair form — never a regex, so a narrower role's
// permission list can never itself become a hand-authored pattern.
type RoutePermission struct {
	Method string
	Path   string
}

// TenantUserPermissions returns every route TenantUser grants: every route
// whose set includes TenantUserRole, which is TenantUserClass and
// TenantAgentClass alike — the machine role's routes are a subset of
// TenantUser's, never a set of their own.
func TenantUserPermissions() []RoutePermission {
	return permissionsFor(TenantUserRole)
}

// TenantAdminPermissions returns every route TenantAdmin grants: everything
// TenantUser grants, plus every TenantAdminOnly route.
func TenantAdminPermissions() []RoutePermission {
	return permissionsFor(TenantAdminRole)
}

// TenantAgentPermissions returns the machine role's routes — the subset of
// TenantUser's reach an environment's own identity holds. See Routes' doc
// comment for the boundary this set draws and why.
func TenantAgentPermissions() []RoutePermission {
	return permissionsFor(TenantAgentRole)
}

// permissionsFor returns every route whose role set includes role.
func permissionsFor(role Roles) []RoutePermission {
	permissions := make([]RoutePermission, 0, len(Routes))
	for key, granted := range Routes {
		if granted&role == 0 {
			continue
		}
		method, path, ok := splitRouteKey(key)
		if !ok {
			continue
		}
		permissions = append(permissions, RoutePermission{Method: method, Path: path})
	}
	return permissions
}

func splitRouteKey(key string) (method string, path string, ok bool) {
	for i := 0; i < len(key); i++ {
		if key[i] == ' ' {
			return key[:i], key[i+1:], true
		}
	}
	return "", "", false
}
