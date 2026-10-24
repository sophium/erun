package platformclient

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// PlatformCreateTenantParams is the operations-only tenant-registration input.
type PlatformCreateTenantParams struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	Issuer        string `json:"issuer"`
	OrgFieldKey   string `json:"orgFieldKey,omitempty"`
	OrgFieldValue string `json:"orgFieldValue,omitempty"`
	DisplayName   string `json:"displayName,omitempty"`
}

// CreateTenant registers a new tenant. Requires an operations-tenant caller.
func (c *PlatformClient) CreateTenant(ctx context.Context, params PlatformCreateTenantParams) (PlatformTenant, error) {
	var tenant PlatformTenant
	err := c.do(ctx, http.MethodPost, "/v1/tenants", params, true, &tenant)
	return tenant, err
}

// ListTenants lists tenants visible to the caller: every tenant for an
// operations-scoped caller, or a single-item list containing only the
// caller's own tenant otherwise.
func (c *PlatformClient) ListTenants(ctx context.Context) ([]PlatformTenant, error) {
	var tenants []PlatformTenant
	err := c.do(ctx, http.MethodGet, "/v1/tenants", nil, true, &tenants)
	return tenants, err
}

// PlatformOrg mirrors zitadel.Org's JSON shape: the platform's own IdP
// organization that an org-scoped tenant's mapping points at.
type PlatformOrg struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PlatformCreateOrgParams is the org-creation input.
type PlatformCreateOrgParams struct {
	Name string `json:"name"`
}

// CreateOrg creates an organization on the platform's own IdP: the org an
// org-scoped tenant's mapping needs before it can resolve any token —
// POST /v1/tenants refuses an org-scoped mapping with no org value, and this
// is the reachable way to obtain one. Requires an operations-tenant caller.
// Pair the returned id with CreateTenant's OrgFieldValue.
func (c *PlatformClient) CreateOrg(ctx context.Context, params PlatformCreateOrgParams) (PlatformOrg, error) {
	var org PlatformOrg
	err := c.do(ctx, http.MethodPost, "/v1/identity/orgs", params, true, &org)
	return org, err
}

// PlatformTenantIssuer mirrors model.TenantIssuer's JSON shape.
type PlatformTenantIssuer struct {
	TenantID      string `json:"tenantId"`
	Issuer        string `json:"issuer"`
	Name          string `json:"name"`
	OrgFieldKey   string `json:"orgFieldKey,omitempty"`
	OrgFieldValue string `json:"orgFieldValue,omitempty"`
}

// PlatformRepairTenantIssuerOrgMappingParams is the repair-path input for a
// tenant already stuck with an unresolvable (issuer, org) mapping. TenantID,
// when set, targets a tenant other than the caller's own and is honored only
// for an operations-scoped caller.
type PlatformRepairTenantIssuerOrgMappingParams struct {
	TenantID      string `json:"tenantId,omitempty"`
	Issuer        string `json:"issuer"`
	OrgFieldKey   string `json:"orgFieldKey"`
	OrgFieldValue string `json:"orgFieldValue"`
}

// RepairTenantIssuerOrgMapping converts issuer to org-scoped (if it is not
// already) and sets the target tenant's own org value, so a tenant created
// with an empty --org-field-value before POST /v1/tenants started refusing
// that (or converted from a single-tenant issuer after a second tenant needed
// one) resolves again. Requires an operations-tenant caller.
func (c *PlatformClient) RepairTenantIssuerOrgMapping(ctx context.Context, params PlatformRepairTenantIssuerOrgMappingParams) (PlatformTenantIssuer, error) {
	var issuer PlatformTenantIssuer
	err := c.do(ctx, http.MethodPatch, "/v1/tenant-issuers", params, true, &issuer)
	return issuer, err
}

// PlatformCreateUserParams is the user-enrollment input. TenantID, when set,
// targets a tenant other than the caller's own and is honored only for an
// operations-scoped caller.
type PlatformCreateUserParams struct {
	Username string `json:"username"`
	Issuer   string `json:"issuer,omitempty"`
	Subject  string `json:"subject,omitempty"`
	TenantID string `json:"tenantId,omitempty"`
	// RoleIDs are the roles the enrollment grants. Empty leaves the platform's
	// own default (TenantUser, or TenantAdmin for a tenant's first user), so
	// naming roles here is what enrolls a tenant's administrator directly
	// instead of a member who then needs elevating from inside the tenant —
	// the elevation an unusable admin makes impossible.
	RoleIDs []string `json:"roleIds,omitempty"`
}

// CreateUser enrolls a user.
func (c *PlatformClient) CreateUser(ctx context.Context, params PlatformCreateUserParams) (PlatformUser, error) {
	var user PlatformUser
	err := c.do(ctx, http.MethodPost, "/v1/users", params, true, &user)
	return user, err
}

// PlatformRolePermission is one permission a role grants: either an exact
// apiMethod/apiPath pair or an apiMethodPattern/apiPathPattern regex pair,
// never both and never neither — the same shape role_permissions stores.
type PlatformRolePermission struct {
	APIMethod        string `json:"apiMethod,omitempty"`
	APIPath          string `json:"apiPath,omitempty"`
	APIMethodPattern string `json:"apiMethodPattern,omitempty"`
	APIPathPattern   string `json:"apiPathPattern,omitempty"`
}

// PlatformRole is a named, tenant-owned bundle of permissions. Name is what an
// operator recognizes; RoleID is what a grant takes, which is why a client
// that can only name the missing access has to resolve one to the other
// before it can hand over a command that runs.
type PlatformRole struct {
	RoleID      string                   `json:"roleId"`
	TenantID    string                   `json:"tenantId,omitempty"`
	Name        string                   `json:"name"`
	Permissions []PlatformRolePermission `json:"permissions"`
}

// Covers reports whether this role would let a caller through to method on to
// apiPath. It mirrors the server's own resolution: an exact pair matches
// literally, a pattern pair matches as compiled regexes, and a permission
// missing either half of its pair never matches anything. An unparseable
// pattern covers nothing rather than everything — a client rendering a remedy
// must not promise access on the strength of a pattern it could not read.
func (r PlatformRole) Covers(method string, apiPath string) bool {
	for _, permission := range r.Permissions {
		if permission.covers(method, apiPath) {
			return true
		}
	}
	return false
}

// covers reports whether this one permission lets the method through, under
// whichever of the two shapes it carries. A permission missing either half of
// its pair covers nothing, and a pattern that does not compile matches
// nothing rather than everything.
func (p PlatformRolePermission) covers(method string, apiPath string) bool {
	if p.APIMethod != "" && p.APIPath != "" {
		return p.APIMethod == method && p.APIPath == apiPath
	}
	if p.APIMethodPattern == "" || p.APIPathPattern == "" {
		return false
	}
	return matchesPattern(p.APIMethodPattern, method) && matchesPattern(p.APIPathPattern, apiPath)
}

func matchesPattern(pattern string, value string) bool {
	matched, err := regexp.MatchString(pattern, value)
	return err == nil && matched
}

// ListRoles lists the caller's tenant's roles. There is no cross-tenant
// override: RLS scopes it to the tenant the caller's token resolved.
func (c *PlatformClient) ListRoles(ctx context.Context) ([]PlatformRole, error) {
	var roles []PlatformRole
	err := c.do(ctx, http.MethodGet, "/v1/roles", nil, true, &roles)
	return roles, err
}

// PlatformGrantUserRoleParams grants one role to one already-enrolled user.
// Unlike enrollment, this is how an operator adds a role to somebody who is
// already in the tenant — the case a client that can see a caller lacks a
// capability has to hand over, since re-enrolling an enrolled identity is a
// no-op that leaves its roles untouched.
type PlatformGrantUserRoleParams struct {
	UserID string `json:"-"`
	RoleID string `json:"roleId"`
}

// GrantUserRole grants RoleID to the user named by UserID, in the caller's own
// resolved tenant (the endpoint has no cross-tenant override; RLS scopes it).
func (c *PlatformClient) GrantUserRole(ctx context.Context, params PlatformGrantUserRoleParams) error {
	path := "/v1/users/" + url.PathEscape(params.UserID) + "/roles"
	return c.do(ctx, http.MethodPost, path, params, true, nil)
}

// PlatformListUsersParams optionally targets another tenant, honored only for
// an operations-scoped caller.
type PlatformListUsersParams struct {
	TenantID string
}

// ListUsers lists the target tenant's users.
func (c *PlatformClient) ListUsers(ctx context.Context, params PlatformListUsersParams) ([]PlatformUser, error) {
	path := "/v1/users"
	if strings.TrimSpace(params.TenantID) != "" {
		path += "?tenantId=" + url.QueryEscape(params.TenantID)
	}
	var users []PlatformUser
	err := c.do(ctx, http.MethodGet, path, nil, true, &users)
	return users, err
}
