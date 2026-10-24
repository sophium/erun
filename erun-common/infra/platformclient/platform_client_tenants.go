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

// CreateTenant registers a new tenant; operations callers only.
func (c *PlatformClient) CreateTenant(ctx context.Context, params PlatformCreateTenantParams) (PlatformTenant, error) {
	var tenant PlatformTenant
	err := c.do(ctx, http.MethodPost, "/v1/tenants", params, true, &tenant)
	return tenant, err
}

// ListTenants lists every tenant for operations callers, else only the caller's own.
func (c *PlatformClient) ListTenants(ctx context.Context) ([]PlatformTenant, error) {
	var tenants []PlatformTenant
	err := c.do(ctx, http.MethodGet, "/v1/tenants", nil, true, &tenants)
	return tenants, err
}

// PlatformOrg mirrors zitadel.Org: the IdP organization an org-scoped tenant maps to.
type PlatformOrg struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type PlatformCreateOrgParams struct {
	Name string `json:"name"`
}

// CreateOrg creates an IdP organization whose id becomes CreateTenant's
// OrgFieldValue; operations callers only.
func (c *PlatformClient) CreateOrg(ctx context.Context, params PlatformCreateOrgParams) (PlatformOrg, error) {
	var org PlatformOrg
	err := c.do(ctx, http.MethodPost, "/v1/identity/orgs", params, true, &org)
	return org, err
}

type PlatformTenantIssuer struct {
	TenantID      string `json:"tenantId"`
	Issuer        string `json:"issuer"`
	Name          string `json:"name"`
	OrgFieldKey   string `json:"orgFieldKey,omitempty"`
	OrgFieldValue string `json:"orgFieldValue,omitempty"`
}

// PlatformRepairTenantIssuerOrgMappingParams repairs an unresolvable (issuer, org)
// mapping; TenantID is honoured only for operations callers.
type PlatformRepairTenantIssuerOrgMappingParams struct {
	TenantID      string `json:"tenantId,omitempty"`
	Issuer        string `json:"issuer"`
	OrgFieldKey   string `json:"orgFieldKey"`
	OrgFieldValue string `json:"orgFieldValue"`
}

// RepairTenantIssuerOrgMapping makes issuer org-scoped and sets the tenant's org value.
func (c *PlatformClient) RepairTenantIssuerOrgMapping(ctx context.Context, params PlatformRepairTenantIssuerOrgMappingParams) (PlatformTenantIssuer, error) {
	var issuer PlatformTenantIssuer
	err := c.do(ctx, http.MethodPatch, "/v1/tenant-issuers", params, true, &issuer)
	return issuer, err
}

// PlatformCreateUserParams.TenantID is honoured only for operations callers.
type PlatformCreateUserParams struct {
	Username string `json:"username"`
	Issuer   string `json:"issuer,omitempty"`
	Subject  string `json:"subject,omitempty"`
	TenantID string `json:"tenantId,omitempty"`
	// RoleIDs empty applies the platform default; naming roles enrolls an admin
	// directly when nobody inside the tenant can elevate one.
	RoleIDs []string `json:"roleIds,omitempty"`
}

func (c *PlatformClient) CreateUser(ctx context.Context, params PlatformCreateUserParams) (PlatformUser, error) {
	var user PlatformUser
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

// PlatformGrantUserRoleParams adds a role to an enrolled user; re-enrolling
// cannot, as it is a no-op.
type PlatformGrantUserRoleParams struct {
	UserID string `json:"-"`
	RoleID string `json:"roleId"`
}

// GrantUserRole grants RoleID to UserID within the caller's tenant.
func (c *PlatformClient) GrantUserRole(ctx context.Context, params PlatformGrantUserRoleParams) error {
	path := "/v1/users/" + url.PathEscape(params.UserID) + "/roles"
	return c.do(ctx, http.MethodPost, path, params, true, nil)
}

// PlatformListUsersParams.TenantID is honoured only for operations callers.
type PlatformListUsersParams struct {
	TenantID string
}

func (c *PlatformClient) ListUsers(ctx context.Context, params PlatformListUsersParams) ([]PlatformUser, error) {
	path := "/v1/users"
	if strings.TrimSpace(params.TenantID) != "" {
		path += "?tenantId=" + url.QueryEscape(params.TenantID)
	}
	var users []PlatformUser
	err := c.do(ctx, http.MethodGet, path, nil, true, &users)
	return users, err
}
