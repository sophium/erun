package repository

import (
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

// TestIssuerMappingIsResolvable locks the one rule both the write refusal and
// Reachable's verdict rest on: an issuer's org-scoping mode and a mapping's
// org value must agree, or nothing can ever resolve through the pair. Empty
// counts as absent on both sides — neither resolution branch matches an empty
// string.
func TestIssuerMappingIsResolvable(t *testing.T) {
	cases := []struct {
		name          string
		orgFieldKey   string
		orgFieldValue string
		want          bool
	}{
		{name: "single-tenant issuer with no org value", want: true},
		{name: "org-scoped issuer with an org value", orgFieldKey: "org_id", orgFieldValue: "42", want: true},
		{name: "org-scoped issuer with no org value", orgFieldKey: "org_id", want: false},
		{name: "org-scoped issuer with a blank org value", orgFieldKey: "org_id", orgFieldValue: "  ", want: false},
		{name: "single-tenant issuer carrying an org value", orgFieldValue: "42", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := issuerMappingIsResolvable(tc.orgFieldKey, tc.orgFieldValue); got != tc.want {
				t.Fatalf("issuerMappingIsResolvable(%q, %q) = %v, want %v", tc.orgFieldKey, tc.orgFieldValue, got, tc.want)
			}
		})
	}
}

// TestAssertResolvableIssuerMappingNamesTheClaimAndTheMode is the refusal an
// operator has to be able to act on: a message that says only "invalid" leaves
// them to work out for themselves that the issuer resolves by an org claim
// their new tenant has no value for.
func TestAssertResolvableIssuerMappingNamesTheClaimAndTheMode(t *testing.T) {
	err := assertResolvableIssuerMapping("https://auth.example", "urn:zitadel:iam:user:resourceowner:id", "")
	if err == nil {
		t.Fatal("expected an org-scoped issuer with no org value to be refused")
	}
	var unresolvable *UnresolvableIssuerMappingError
	if !errors.As(err, &unresolvable) {
		t.Fatalf("error is not an *UnresolvableIssuerMappingError: %v", err)
	}
	if unresolvable.Reason != model.TenantReachabilityNoOrgMapping {
		t.Fatalf("reason = %q, want %q", unresolvable.Reason, model.TenantReachabilityNoOrgMapping)
	}
	for _, want := range []string{"https://auth.example", "urn:zitadel:iam:user:resourceowner:id"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("message %q does not name %q", err.Error(), want)
		}
	}
}

// TestAssertResolvableIssuerMappingNamesTheOrgCreateCommand covers a refusal
// that names the claim but leaves an operator with no way to actually obtain
// an org value. The message must point at the command that closes that gap.
func TestAssertResolvableIssuerMappingNamesTheOrgCreateCommand(t *testing.T) {
	err := assertResolvableIssuerMapping("https://auth.example", "urn:zitadel:iam:user:resourceowner:id", "")
	if err == nil {
		t.Fatal("expected an org-scoped issuer with no org value to be refused")
	}
	if !strings.Contains(err.Error(), "erun platform identity org create") {
		t.Fatalf("message %q does not name the org-create command", err.Error())
	}
}

// TestAssertResolvableIssuerMappingRefusesAnOrgValueOnASingleTenantIssuer is
// the mirror case: the value is read by nothing, so the mapping is just as
// dead, and the message must say which way round the mismatch is rather than
// reusing the org-scoped wording.
func TestAssertResolvableIssuerMappingRefusesAnOrgValueOnASingleTenantIssuer(t *testing.T) {
	err := assertResolvableIssuerMapping("https://idp.example", "", "386994597030592700")
	if err == nil {
		t.Fatal("expected an org value under a single-tenant issuer to be refused")
	}
	if !strings.Contains(err.Error(), "single-tenant") || !strings.Contains(err.Error(), "386994597030592700") {
		t.Fatalf("message %q does not name the mode and the org value", err.Error())
	}
}

func TestAssertResolvableIssuerMappingAcceptsAConsistentMapping(t *testing.T) {
	if err := assertResolvableIssuerMapping("https://idp.example", "", ""); err != nil {
		t.Fatalf("single-tenant mapping refused: %v", err)
	}
	if err := assertResolvableIssuerMapping("https://auth.example", "org_id", "42"); err != nil {
		t.Fatalf("org-scoped mapping refused: %v", err)
	}
}

// TestUnresolvableIssuerMappingErrorReportsAnUnmappedIssuer covers the
// enrollment-side reason: the target tenant has no mapping for this issuer at
// all, which used to surface as a bare foreign-key violation.
func TestUnresolvableIssuerMappingErrorReportsAnUnmappedIssuer(t *testing.T) {
	err := &UnresolvableIssuerMappingError{Issuer: "https://idp.example", Reason: model.TenantReachabilityIssuerNotMapped}
	if !strings.Contains(err.Error(), "not mapped to this tenant") {
		t.Fatalf("message %q does not report the unmapped issuer", err.Error())
	}
}
