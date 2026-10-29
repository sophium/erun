package model

import (
	"time"

	"github.com/uptrace/bun"
)

type TenantType string

const (
	TenantTypeOperations TenantType = "OPERATIONS"
	TenantTypeCompany    TenantType = "COMPANY"
)

// TenantReachability reports whether a sign-in by the caller's identity can
// resolve this tenant; membership alone does not guarantee it.
type TenantReachability string

const (
	TenantReachabilityResolvable  TenantReachability = "RESOLVABLE"
	TenantReachabilityOrgMismatch TenantReachability = "ORG_MISMATCH"
	// TenantReachabilityNoOrgMapping means no token at all can satisfy the mapping.
	TenantReachabilityNoOrgMapping    TenantReachability = "NO_ORG_MAPPING"
	TenantReachabilityIssuerNotMapped TenantReachability = "ISSUER_NOT_MAPPED"
)

type Tenant struct {
	bun.BaseModel `bun:"table:tenants,alias:t"`
	TenantID      string     `json:"tenantId" bun:"tenant_id,pk,scanonly"`
	Name          string     `json:"name" bun:"name"`
	Type          TenantType `json:"type" bun:"type"`
	CreatedAt     time.Time  `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt     time.Time  `json:"updatedAt" bun:"updated_at,scanonly"`
	// UserCount is nil, not zero, where a query does not compute it.
	UserCount *int `json:"userCount,omitempty" bun:"user_count,scanonly"`
	// Reachability is set only by TenantRepository.Reachable.
	Reachability TenantReachability `json:"reachability,omitempty" bun:"reachability,scanonly"`
	// Resolvable is set only by TenantRepository.List's operations branch; false
	// means no token can reach the tenant.
	Resolvable *bool `json:"resolvable,omitempty" bun:"resolvable,scanonly"`
}

type TenantIssuer struct {
	bun.BaseModel `bun:"table:tenant_issuers,alias:ti"`
	TenantID      string `json:"tenantId" bun:"tenant_id,scanonly"`
	Issuer        string `json:"issuer" bun:"issuer,pk,scanonly"`
	Name          string `json:"name" bun:"name"`
	// OrgFieldKey belongs to the shared issuer row; empty means a single-tenant issuer.
	OrgFieldKey   string    `json:"orgFieldKey,omitempty" bun:"org_field_key,scanonly"`
	OrgFieldValue string    `json:"orgFieldValue,omitempty" bun:"org_field_value,scanonly"`
	CreatedAt     time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt     time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
