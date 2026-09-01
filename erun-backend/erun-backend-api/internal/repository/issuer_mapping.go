package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

// UnresolvableIssuerMappingError refuses identity configuration no token can
// ever satisfy. Resolution reads (issuer, org claim); a mapping whose org
// value contradicts its issuer's org-scoping mode is matched by nothing, so a
// tenant registered that way exists, lists, and accepts enrollments while
// remaining permanently unreachable. Writing it and discovering that only
// when someone fails to sign in is the silence this refusal replaces.
//
// Reason reuses model.TenantReachability's vocabulary deliberately: the code
// an operator sees when a switch target turns out to be unreachable is the
// same code they see when the platform refuses to create one.
type UnresolvableIssuerMappingError struct {
	Issuer        string
	Reason        model.TenantReachability
	OrgFieldKey   string
	OrgFieldValue string
}

func (e *UnresolvableIssuerMappingError) Error() string {
	if e.Reason == model.TenantReachabilityIssuerNotMapped {
		return fmt.Sprintf("issuer %q is not mapped to this tenant, so no token from it can resolve here", e.Issuer)
	}
	if e.OrgFieldKey != "" {
		return fmt.Sprintf(
			"issuer %q resolves tenants by the %q claim, so this tenant's mapping needs the org value that claim will carry; it has none, and no token can resolve to it",
			e.Issuer, e.OrgFieldKey)
	}
	return fmt.Sprintf(
		"issuer %q is registered single-tenant (it carries no org claim), so the org value %q on this tenant's mapping is read by nothing and no token can resolve to it",
		e.Issuer, e.OrgFieldValue)
}

// sqlIssuerMappingIsResolvable is the predicate deciding whether an
// (issuers i, tenant_issuers ti) pair is one some token could actually resolve
// through: the issuer's org-scoping mode and the mapping's org value must
// agree. An org-scoped issuer whose mapping carries no org value is dead
// (resolution matches the org by equality, and an empty org claim is rejected
// before it gets that far), and so is an org value under a single-tenant
// issuer (resolution matches NULL there). An empty string counts as absent on
// both sides for the same reason: neither resolution branch can ever match one.
const sqlIssuerMappingIsResolvable = `((i.org_field_key IS NULL OR i.org_field_key = '') = (ti.org_field_value IS NULL OR ti.org_field_value = ''))`

// issuerMappingIsResolvable is the Go-side half of the same rule, for writes
// that hold the two values directly rather than reading them back as rows.
func issuerMappingIsResolvable(orgFieldKey, orgFieldValue string) bool {
	return (strings.TrimSpace(orgFieldKey) == "") == (strings.TrimSpace(orgFieldValue) == "")
}

// assertResolvableIssuerMapping refuses an (issuer, org value) pair that
// contradicts the issuer's registered org-scoping mode. orgFieldKey is the
// issuer's effective mode as the registry actually holds it — never the value
// a caller asked for, which an already-registered issuer ignores.
func assertResolvableIssuerMapping(issuer, orgFieldKey, orgFieldValue string) error {
	if issuerMappingIsResolvable(orgFieldKey, orgFieldValue) {
		return nil
	}
	return &UnresolvableIssuerMappingError{
		Issuer:        strings.TrimSpace(issuer),
		Reason:        model.TenantReachabilityNoOrgMapping,
		OrgFieldKey:   strings.TrimSpace(orgFieldKey),
		OrgFieldValue: strings.TrimSpace(orgFieldValue),
	}
}

// assertTenantIssuerMappingResolvable refuses linking an external identity
// into a tenant the issuer cannot resolve to. Enrollment is what makes a token
// usable, so a membership row under a dead mapping is a user who can never
// sign in — created successfully, failing only later and somewhere else.
func assertTenantIssuerMappingResolvable(ctx context.Context, tx bun.Tx, tenantID, issuer string) error {
	var mapping struct {
		OrgFieldKey   string `bun:"org_field_key"`
		OrgFieldValue string `bun:"org_field_value"`
	}
	err := tx.NewRaw(`
		SELECT COALESCE(i.org_field_key, '') AS org_field_key,
		       COALESCE(ti.org_field_value, '') AS org_field_value
		  FROM tenant_issuers ti
		  LEFT JOIN issuers i ON i.issuer = ti.issuer
		 WHERE ti.tenant_id = ? AND ti.issuer = ?
	`, tenantID, issuer).Scan(ctx, &mapping)
	if err != nil {
		if normalizeNoRows(err) == ErrNotFound {
			return &UnresolvableIssuerMappingError{
				Issuer: strings.TrimSpace(issuer),
				Reason: model.TenantReachabilityIssuerNotMapped,
			}
		}
		return err
	}
	return assertResolvableIssuerMapping(issuer, mapping.OrgFieldKey, mapping.OrgFieldValue)
}
