package model

// PlatformCreateTenantParams is the operations-only tenant-registration input.
type PlatformCreateTenantParams struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	Issuer        string `json:"issuer"`
	OrgFieldKey   string `json:"orgFieldKey,omitempty"`
	OrgFieldValue string `json:"orgFieldValue,omitempty"`
	DisplayName   string `json:"displayName,omitempty"`
}

// PlatformOrg mirrors zitadel.Org: the IdP organization an org-scoped tenant maps to.
type PlatformOrg struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type PlatformCreateOrgParams struct {
	Name string `json:"name"`
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

// PlatformGrantUserRoleParams adds a role to an enrolled user; re-enrolling
// cannot, as it is a no-op.
type PlatformGrantUserRoleParams struct {
	UserID string `json:"-"`
	RoleID string `json:"roleId"`
}

// PlatformListUsersParams.TenantID is honoured only for operations callers.
type PlatformListUsersParams struct {
	TenantID string
}
