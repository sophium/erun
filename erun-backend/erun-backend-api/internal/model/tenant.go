package model

import "time"

type TenantType string

const (
	TenantTypeOperations TenantType = "OPERATIONS"
	TenantTypeCompany    TenantType = "COMPANY"
)

type Tenant struct {
	TenantID  string     `json:"tenantId"`
	Name      string     `json:"name"`
	Type      TenantType `json:"type"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}
