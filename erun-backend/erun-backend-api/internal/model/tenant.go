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

// TenantReachability answers, for one membership the caller actually holds,
// whether a sign-in by that same identity can produce this tenant. Membership
// and resolution are different keys — membership is (issuer, external_id),
// resolution is (issuer, org claim) — so holding a membership row is
// necessary but not sufficient, and a client that treats the two as the same
// thing offers a switch target no token can ever reach.
type TenantReachability string

const (
	// TenantReachabilityResolvable means the caller's own presented
	// (issuer, org) resolves to this tenant: signing in again lands here.
	TenantReachabilityResolvable TenantReachability = "RESOLVABLE"
	// TenantReachabilityOrgMismatch means this tenant resolves for a
	// different org value than the one this identity presents. The membership
	// row is real and permanently unusable by this identity.
	TenantReachabilityOrgMismatch TenantReachability = "ORG_MISMATCH"
	// TenantReachabilityNoOrgMapping means the tenant's mapping for this
	// issuer carries no org value while the issuer resolves tenants by org
	// (or carries one while the issuer does not) — the mapping no token at
	// all can satisfy, not merely this caller's.
	TenantReachabilityNoOrgMapping TenantReachability = "NO_ORG_MAPPING"
	// TenantReachabilityIssuerNotMapped means the tenant has no mapping for
	// the issuer this identity signs in through.
	TenantReachabilityIssuerNotMapped TenantReachability = "ISSUER_NOT_MAPPED"
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
	// Reachability is read-only, per-caller data populated only by
	// TenantRepository.Reachable — empty, never RESOLVABLE, on every other
	// read path, so a caller can tell "this membership resolves" apart from
	// "this read never asked".
	Reachability TenantReachability `json:"reachability,omitempty" bun:"reachability,scanonly"`
	// Resolvable is read-only, per-tenant configuration data populated only by
	// TenantRepository.List's operations branch: whether any registered issuer
	// mapping for this tenant can resolve a token at all, for anyone. nil
	// wherever it was not computed — false means the tenant is unreachable by
	// construction and needs its issuer mapping repaired, which is a fact an
	// operator has to be shown rather than left to infer from an empty list.
	Resolvable *bool `json:"resolvable,omitempty" bun:"resolvable,scanonly"`
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
