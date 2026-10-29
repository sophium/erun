package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

// UnresolvableIssuerMappingError refuses an issuer mapping no token can ever
// resolve through. Reason reuses model.TenantReachability's vocabulary.
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
			"issuer %q resolves tenants by the %q claim, so this tenant's mapping needs the org value that claim will carry; it has none, and no token can resolve to it. "+
				"Create one with `erun platform identity org create --name <org-name>` and pass its id as --org-field-value.",
			e.Issuer, e.OrgFieldKey)
	}
	return fmt.Sprintf(
		"issuer %q is registered single-tenant (it carries no org claim), so the org value %q on this tenant's mapping is read by nothing and no token can resolve to it",
		e.Issuer, e.OrgFieldValue)
}

// sqlIssuerMappingIsResolvable requires the issuer's org-scoping mode and the
// mapping's org value to agree; empty counts as absent on both sides.
const sqlIssuerMappingIsResolvable = `((i.org_field_key IS NULL OR i.org_field_key = '') = (ti.org_field_value IS NULL OR ti.org_field_value = ''))`

// issuerMappingIsResolvable is the Go-side twin of sqlIssuerMappingIsResolvable.
func issuerMappingIsResolvable(orgFieldKey, orgFieldValue string) bool {
	return (strings.TrimSpace(orgFieldKey) == "") == (strings.TrimSpace(orgFieldValue) == "")
}

// assertResolvableIssuerMapping refuses a mismatched pair. orgFieldKey must be
// the registry's effective mode, not the value the caller requested.
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

// assertTenantIssuerMappingResolvable refuses enrolling a user under a mapping
// that can never resolve, since that user could never sign in.
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
		if errors.Is(normalizeNoRows(err), ErrNotFound) {
			return &UnresolvableIssuerMappingError{
				Issuer: strings.TrimSpace(issuer),
				Reason: model.TenantReachabilityIssuerNotMapped,
			}
		}
		return err
	}
	return assertResolvableIssuerMapping(issuer, mapping.OrgFieldKey, mapping.OrgFieldValue)
}
