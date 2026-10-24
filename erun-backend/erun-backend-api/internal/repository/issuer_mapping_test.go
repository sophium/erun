package repository

import (
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

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

func TestAssertResolvableIssuerMappingNamesTheOrgCreateCommand(t *testing.T) {
	err := assertResolvableIssuerMapping("https://auth.example", "urn:zitadel:iam:user:resourceowner:id", "")
	if err == nil {
		t.Fatal("expected an org-scoped issuer with no org value to be refused")
	}
	if !strings.Contains(err.Error(), "erun platform identity org create") {
		t.Fatalf("message %q does not name the org-create command", err.Error())
	}
}

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

func TestUnresolvableIssuerMappingErrorReportsAnUnmappedIssuer(t *testing.T) {
	err := &UnresolvableIssuerMappingError{Issuer: "https://idp.example", Reason: model.TenantReachabilityIssuerNotMapped}
	if !strings.Contains(err.Error(), "not mapped to this tenant") {
		t.Fatalf("message %q does not report the unmapped issuer", err.Error())
	}
}
