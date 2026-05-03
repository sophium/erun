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
}

type TenantIssuer struct {
	bun.BaseModel `bun:"table:tenant_issuers,alias:ti"`
	TenantID      string    `json:"tenantId" bun:"tenant_id,scanonly"`
	Issuer        string    `json:"issuer" bun:"issuer,pk,scanonly"`
	Name          string    `json:"name" bun:"name"`
	CreatedAt     time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt     time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
