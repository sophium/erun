// Package routeroles classifies every protected route by the narrower predefined
// roles that grant it. It is a leaf package so repository can import it.
package routeroles

// Roles is a membership set because TenantAgent is a strict subset of TenantUser.
type Roles uint8

const (
	// TenantUserRole reads the tenant and drives reviews, builds and existing environments.
	TenantUserRole Roles = 1 << iota
	// TenantAdminRole administers the tenant, never operations.
	TenantAdminRole
	// TenantAgentRole is the environment machine role; extend it only for a real caller.
	TenantAgentRole
)

const (
	// OperationsOnly routes are reachable only via ReadAll/WriteAll.
	OperationsOnly Roles = 0
	// TenantUserClass is granted by TenantUser and TenantAdmin.
	TenantUserClass Roles = TenantUserRole | TenantAdminRole
	// TenantAdminOnly is granted by TenantAdmin alone.
	TenantAdminOnly Roles = TenantAdminRole
	// TenantAgentClass is granted by all three narrower roles.
	TenantAgentClass Roles = TenantUserRole | TenantAdminRole | TenantAgentRole
)

// Routes classifies every registered protected route, keyed "<METHOD> <path
// template>" as passed to ProtectedRouteRegistrar. erun-integration fails when
// a registered route is missing. TenantAgent gets POST /v1/builds (what `erun
// build` calls) but not the tenant-wide GET.
var Routes = map[string]Roles{
	"GET /v1/whoami": TenantAgentClass,

	"GET /v1/config": TenantUserClass,

	"GET /v1/tenants":                            TenantUserClass,
	"GET /v1/tenants/reachable":                  TenantUserClass,
	"POST /v1/tenants":                           OperationsOnly,
	"PATCH /v1/tenants/reconcile-bootstrap-name": OperationsOnly,

	"GET /v1/quota":                     TenantUserClass,
	"PUT /v1/tenants/{tenant_id}/quota": OperationsOnly,

	// PATCH also serves the operations-only org-scope conversion.
	"GET /v1/tenant-issuers":   OperationsOnly,
	"PATCH /v1/tenant-issuers": OperationsOnly,

	"GET /v1/usage-events": TenantUserClass,
	"GET /v1/audit-events": TenantUserClass,

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

	"GET /v1/reviews/{review_id}/builds":            TenantAgentClass,
	"POST /v1/reviews/{review_id}/builds":           TenantAgentClass,
	"GET /v1/reviews/{review_id}/builds/{build_id}": TenantUserClass,
	"GET /v1/builds":                                TenantUserClass,
	"POST /v1/builds":                               TenantAgentClass,

	"GET /v1/events": TenantUserClass,
	"POST /v1/environments/{environment_id}/events": TenantUserClass,

	"GET /v1/reviews/{review_id}/comments":                       TenantAgentClass,
	"POST /v1/reviews/{review_id}/comments":                      TenantUserClass,
	"PATCH /v1/reviews/{review_id}/comments/{comment_id}/status": TenantUserClass,

	"GET /v1/releases":                     TenantUserClass,
	"POST /v1/releases":                    TenantUserClass,
	"GET /v1/releases/{release_id}":        TenantUserClass,
	"GET /v1/reviews/{review_id}/releases": TenantUserClass,

	"GET /v1/environments":                          TenantUserClass,
	"POST /v1/environments":                         TenantAdminOnly,
	"GET /v1/environments/{environment_id}":         TenantUserClass,
	"POST /v1/environments/{environment_id}/deploy": TenantUserClass,
	"POST /v1/environments/{environment_id}/stop":   TenantUserClass,
	"DELETE /v1/environments/{environment_id}":      TenantAdminOnly,

	// machine-identity is TenantUser on purpose: its credential is weaker than an
	// mcp-token, and provisioning runs under an ordinary operator's alias.
	"POST /v1/environments/{environment_id}/dns01-token":      TenantUserClass,
	"POST /v1/environments/{environment_id}/mcp-token":        TenantUserClass,
	"POST /v1/environments/{environment_id}/machine-identity": TenantUserClass,

	"PUT /v1/environments/{environment_id}/hostname":    TenantUserClass,
	"DELETE /v1/environments/{environment_id}/hostname": TenantUserClass,

	"POST /v1/environments/{environment_id}/ai-sessions": TenantUserClass,
	"GET /v1/environments/{environment_id}/ai-sessions":  TenantUserClass,

	"PUT /v1/environments/{environment_id}/definition": TenantUserClass,
	"GET /v1/environments/{environment_id}/definition": TenantUserClass,

	"GET /v1/jobs":                               TenantUserClass,
	"POST /v1/jobs":                              TenantUserClass,
	"GET /v1/jobs/{job_id}":                      TenantUserClass,
	"PATCH /v1/jobs/{job_id}":                    TenantUserClass,
	"GET /v1/environments/{environment_id}/jobs": TenantUserClass,

	"GET /v1/pipeline": TenantUserClass,

	"GET /v1/contexts":              TenantUserClass,
	"POST /v1/contexts":             TenantAdminOnly,
	"GET /v1/contexts/{context_id}": TenantUserClass,

	"PUT /v1/cloud-provider-aliases/{alias}": TenantAdminOnly,

	"POST /v1/provision": TenantAdminOnly,

	"GET /v1/roles":                              TenantUserClass,
	"POST /v1/roles":                             TenantAdminOnly,
	"GET /v1/users/{user_id}/roles":              TenantUserClass,
	"POST /v1/users/{user_id}/roles":             TenantAdminOnly,
	"DELETE /v1/users/{user_id}/roles/{role_id}": TenantAdminOnly,

	"GET /v1/users":  TenantUserClass,
	"POST /v1/users": TenantAdminOnly,

	"GET /v1/identity/users":                           OperationsOnly,
	"POST /v1/identity/users":                          OperationsOnly,
	"POST /v1/identity/users/{external_id}/deactivate": OperationsOnly,
	"POST /v1/identity/users/{external_id}/reactivate": OperationsOnly,
	"POST /v1/identity/orgs":                           OperationsOnly,
	"GET /v1/identity/org-settings":                    OperationsOnly,
	"PATCH /v1/identity/org-settings":                  OperationsOnly,
	"GET /v1/identity/smtp-settings":                   OperationsOnly,
	"PATCH /v1/identity/smtp-settings":                 OperationsOnly,

	"POST /v1/invites":               TenantAdminOnly,
	"GET /v1/invites":                TenantUserClass,
	"DELETE /v1/invites/{invite_id}": TenantAdminOnly,

	// The unauthenticated invite-request routes never reach PermissionAuthorizer.
	"GET /v1/invite-requests":                              TenantUserClass,
	"POST /v1/invite-requests/{invite_request_id}/approve": TenantAdminOnly,
	"POST /v1/invite-requests/{invite_request_id}/decline": TenantAdminOnly,

	"PATCH /v1/config/invite-request-rate-limit": OperationsOnly,
}

// RoutePermission is one exact (method, path) grant, never a pattern.
type RoutePermission struct {
	Method string
	Path   string
}

// TenantUserPermissions returns every route whose set includes TenantUserRole.
func TenantUserPermissions() []RoutePermission {
	return permissionsFor(TenantUserRole)
}

// TenantAdminPermissions returns every route TenantAdmin grants.
func TenantAdminPermissions() []RoutePermission {
	return permissionsFor(TenantAdminRole)
}

// TenantAgentPermissions returns the machine role's routes.
func TenantAgentPermissions() []RoutePermission {
	return permissionsFor(TenantAgentRole)
}

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
