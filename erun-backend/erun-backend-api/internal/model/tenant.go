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

type Tenant struct {
	bun.BaseModel `bun:"table:tenants,alias:t"`
	TenantID      string     `json:"tenantId" bun:"tenant_id,pk,scanonly"`
	Name          string     `json:"name" bun:"name"`
	Type          TenantType `json:"type" bun:"type"`
	CreatedAt     time.Time  `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt     time.Time  `json:"updatedAt" bun:"updated_at,scanonly"`
	// UserCount is read-only, UX-derived data populated only by queries that
	// join against users (TenantRepository.List) — nil, not zero, wherever a
	// query does not compute it, so a caller can tell "this tenant genuinely
	// has zero users" apart from "this read path never counted".
	UserCount *int `json:"userCount,omitempty" bun:"user_count,scanonly"`
}

type TenantIssuer struct {
	bun.BaseModel `bun:"table:tenant_issuers,alias:ti"`
	TenantID      string `json:"tenantId" bun:"tenant_id,scanonly"`
	Issuer        string `json:"issuer" bun:"issuer,pk,scanonly"`
	Name          string `json:"name" bun:"name"`
	// OrgFieldKey is the issuer's org-scoping mode, read from the shared
	// issuers row: the token claim whose value selects a tenant. Empty means a
	// single-tenant issuer, where iss alone resolves. Read-only here — it
	// belongs to the issuer, not to one tenant's mapping.
	OrgFieldKey string `json:"orgFieldKey,omitempty" bun:"org_field_key,scanonly"`
	// OrgFieldValue is this mapping's org value under an org-scoped issuer.
	OrgFieldValue string    `json:"orgFieldValue,omitempty" bun:"org_field_value,scanonly"`
	CreatedAt     time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt     time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
