package main

import (
	"strings"
	"testing"
)

func TestTenantReviewCreateCapabilityAllowsWhenGranted(t *testing.T) {
	var requests []string
	capabilities := `[{"method":"GET","path":"/v1/whoami"},{"method":"POST","path":"/v1/reviews"}]`
	server := tenantDashboardAPI(t, capabilities, nil, &requests)
	defer server.Close()

	capability, err := tenantDashboardApp(t, server.URL).TenantReviewCreateCapability("frs")
	if err != nil {
		t.Fatalf("TenantReviewCreateCapability failed: %v", err)
	}
	if !capability.CanCreate || capability.Restricted != "" {
		t.Fatalf("expected an allowed capability, got %+v", capability)
	}
	for _, path := range requests {
		if path != "/v1/whoami" {
			t.Fatalf("expected only /v1/whoami to be read, got %v", requests)
		}
	}
}

func TestTenantReviewCreateCapabilityRestrictedWhenNotGranted(t *testing.T) {
	var requests []string
	capabilities := `[{"method":"GET","path":"/v1/whoami"}]`
	server := tenantDashboardAPI(t, capabilities, nil, &requests)
	defer server.Close()

	capability, err := tenantDashboardApp(t, server.URL).TenantReviewCreateCapability("frs")
	if err != nil {
		t.Fatalf("TenantReviewCreateCapability failed: %v", err)
	}
	if capability.CanCreate || capability.Restricted == "" {
		t.Fatalf("expected a restricted capability naming why, got %+v", capability)
	}
}

func TestTenantReviewCreateCapabilityRequiresATenant(t *testing.T) {
	server := tenantDashboardAPI(t, "null", nil, &[]string{})
	defer server.Close()

	_, err := tenantDashboardApp(t, server.URL).TenantReviewCreateCapability("")
	if err == nil || !strings.Contains(err.Error(), "tenant is required") {
		t.Fatalf("expected a tenant-required error, got %v", err)
	}
}
