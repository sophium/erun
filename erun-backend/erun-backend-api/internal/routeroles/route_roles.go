// Package routeroles is the single source of truth for which of the two
// narrower predefined roles (TenantUser, TenantAdmin) grant which registered
// API route. It exists in its own leaf package, rather than inside
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

// Class classifies one registered route against the two narrower predefined
// roles.
type Class int

const (
	// OperationsOnly means neither TenantUser nor TenantAdmin grants this
	// route. It stays reachable only through the wildcard ReadAll/WriteAll
	// roles — a platform operator — even from inside an OPERATIONS tenant.
	// Every route classified here is already gated at the handler by tenant
	// type (or, for GET/PATCH /v1/tenant-issuers, shares a route with a half
	// that is), so this classification is the role-permission layer's half
	// of that same defense in depth.
	OperationsOnly Class = iota
	// TenantAdminOnly means TenantAdmin grants this route and TenantUser
	// does not: full tenant administration (environments, contexts, users,
	// invites, roles), never operations.
	TenantAdminOnly
	// TenantUserClass means both TenantUser and TenantAdmin grant this
	// route: reading the tenant, and driving reviews/comments/builds/the
	// merge queue and existing environments.
	TenantUserClass
)

// Routes classifies every registered protected API route, keyed
// "<METHOD> <canonical path template>" exactly as passed to
// ProtectedRouteRegistrar — the same key shape route_audit.go's
// InternalAPIRoutes/KnownUnsurfacedRoutes use. Grouped by the route file
// each entry comes from so a reviewer can diff this against
// internal/routes/*.go directly.
var Routes = map[string]Class{
	// whoami.go — every authenticated caller needs this to learn their own
	// capabilities, regardless of role.
	"GET /v1/whoami": TenantUserClass,

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
	// TenantUser is for.
	"GET /v1/reviews":                                    TenantUserClass,
	"POST /v1/reviews":                                   TenantUserClass,
	"GET /v1/reviews/merge-queue":                        TenantUserClass,
	"POST /v1/reviews/merge-queue/advance":               TenantUserClass,
	"POST /v1/reviews/merge-queue/override-advance":      TenantUserClass,
	"GET /v1/reviews/{review_id}":                        TenantUserClass,
	"PATCH /v1/reviews/{review_id}/status":               TenantUserClass,
	"GET /v1/reviews/{review_id}/reviewers":              TenantUserClass,
	"POST /v1/reviews/{review_id}/reviewers":             TenantUserClass,
	"DELETE /v1/reviews/{review_id}/reviewers/{user_id}": TenantUserClass,

	// builds.go — reporting/reading a review's builds is part of driving it.
	"GET /v1/reviews/{review_id}/builds":            TenantUserClass,
	"POST /v1/reviews/{review_id}/builds":           TenantUserClass,
	"GET /v1/reviews/{review_id}/builds/{build_id}": TenantUserClass,

	// comments.go — driving review comments.
	"GET /v1/reviews/{review_id}/comments":                       TenantUserClass,
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

	// ai_sessions.go — an environment self-reporting its own AI-session status
	// is the same class as reporting a build result: operating an environment
	// that already exists, not administering the tenant. Reading it back is
	// the same class as reading the environment itself.
	"POST /v1/environments/{environment_id}/ai-sessions": TenantUserClass,
	"GET /v1/environments/{environment_id}/ai-sessions":  TenantUserClass,

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

// TenantUserPermissions returns every route classified TenantUserClass.
func TenantUserPermissions() []RoutePermission {
	return permissionsFor(func(class Class) bool { return class == TenantUserClass })
}

// TenantAdminPermissions returns every route TenantAdmin grants: everything
// TenantUser grants, plus every TenantAdminOnly route.
func TenantAdminPermissions() []RoutePermission {
	return permissionsFor(func(class Class) bool { return class == TenantUserClass || class == TenantAdminOnly })
}

func permissionsFor(include func(Class) bool) []RoutePermission {
	permissions := make([]RoutePermission, 0, len(Routes))
	for key, class := range Routes {
		if !include(class) {
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
