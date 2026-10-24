package platformclient

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
)

// CreateTenant registers a new tenant; operations callers only.
func (c *PlatformClient) CreateTenant(ctx context.Context, params model.PlatformCreateTenantParams) (model.PlatformTenant, error) {
	var tenant model.PlatformTenant
	err := c.do(ctx, http.MethodPost, "/v1/tenants", params, true, &tenant)
	return tenant, err
}

// ListTenants lists every tenant for operations callers, else only the caller's own.
func (c *PlatformClient) ListTenants(ctx context.Context) ([]model.PlatformTenant, error) {
	var tenants []model.PlatformTenant
	err := c.do(ctx, http.MethodGet, "/v1/tenants", nil, true, &tenants)
	return tenants, err
}

// CreateOrg creates an IdP organization whose id becomes CreateTenant's
// OrgFieldValue; operations callers only.
func (c *PlatformClient) CreateOrg(ctx context.Context, params model.PlatformCreateOrgParams) (model.PlatformOrg, error) {
	var org model.PlatformOrg
	err := c.do(ctx, http.MethodPost, "/v1/identity/orgs", params, true, &org)
	return org, err
}

// RepairTenantIssuerOrgMapping makes issuer org-scoped and sets the tenant's org value.
func (c *PlatformClient) RepairTenantIssuerOrgMapping(ctx context.Context, params model.PlatformRepairTenantIssuerOrgMappingParams) (model.PlatformTenantIssuer, error) {
	var issuer model.PlatformTenantIssuer
	err := c.do(ctx, http.MethodPatch, "/v1/tenant-issuers", params, true, &issuer)
	return issuer, err
}

func (c *PlatformClient) CreateUser(ctx context.Context, params model.PlatformCreateUserParams) (model.PlatformUser, error) {
	var user model.PlatformUser
	err := c.do(ctx, http.MethodPost, "/v1/users", params, true, &user)
	return user, err
}

// PlatformRolePermission is an exact pair or a regex pair, never both or neither.
type PlatformRolePermission struct {
	APIMethod        string `json:"apiMethod,omitempty"`
	APIPath          string `json:"apiPath,omitempty"`
	APIMethodPattern string `json:"apiMethodPattern,omitempty"`
	APIPathPattern   string `json:"apiPathPattern,omitempty"`
}

// PlatformRole is a tenant-owned bundle of permissions; grants take RoleID.
type PlatformRole struct {
	RoleID      string                   `json:"roleId"`
	TenantID    string                   `json:"tenantId,omitempty"`
	Name        string                   `json:"name"`
	Permissions []PlatformRolePermission `json:"permissions"`
}

// Covers mirrors the server's resolution. An unparseable pattern covers nothing,
// so a remedy never promises access it cannot confirm.
func (r PlatformRole) Covers(method string, apiPath string) bool {
	for _, permission := range r.Permissions {
		if permission.covers(method, apiPath) {
			return true
		}
	}
	return false
}

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

// ListRoles lists the caller's tenant's roles; RLS forbids cross-tenant reads.
func (c *PlatformClient) ListRoles(ctx context.Context) ([]PlatformRole, error) {
	var roles []PlatformRole
	err := c.do(ctx, http.MethodGet, "/v1/roles", nil, true, &roles)
	return roles, err
}

// GrantUserRole grants RoleID to UserID within the caller's tenant.
func (c *PlatformClient) GrantUserRole(ctx context.Context, params model.PlatformGrantUserRoleParams) error {
	path := "/v1/users/" + url.PathEscape(params.UserID) + "/roles"
	return c.do(ctx, http.MethodPost, path, params, true, nil)
}

func (c *PlatformClient) ListUsers(ctx context.Context, params model.PlatformListUsersParams) ([]model.PlatformUser, error) {
	path := "/v1/users"
	if strings.TrimSpace(params.TenantID) != "" {
		path += "?tenantId=" + url.QueryEscape(params.TenantID)
	}
	var users []model.PlatformUser
	err := c.do(ctx, http.MethodGet, path, nil, true, &users)
	return users, err
}
