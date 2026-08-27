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
	// PlatformDeclaredName is read-only display data populated only by
	// TenantRepository.Current, and only when the caller's own tenant is
	// OPERATIONS and its Name disagrees with this platform's declared
	// identity (ERUN_TENANT). It is what surfaces that disagreement to an
	// operator instead of leaving it discoverable only by querying the
	// database directly.
	PlatformDeclaredName string `json:"platformDeclaredName,omitempty" bun:"-"`
}

type TenantIssuer struct {
	bun.BaseModel `bun:"table:tenant_issuers,alias:ti"`
	TenantID      string    `json:"tenantId" bun:"tenant_id,scanonly"`
	Issuer        string    `json:"issuer" bun:"issuer,pk,scanonly"`
	Name          string    `json:"name" bun:"name"`
	CreatedAt     time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt     time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
