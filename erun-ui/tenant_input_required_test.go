package main

import (
	"errors"
	"strings"
	"testing"
)

func TestRequireTenantTrimsAndPassesThrough(t *testing.T) {
	tenant, err := requireTenant("loading the tenant dashboard", "  frs  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tenant != "frs" {
		t.Fatalf("expected a trimmed tenant, got %q", tenant)
	}
}

func TestRequireTenantNamesOperationAndRecoveryWhenEmpty(t *testing.T) {
	_, err := requireTenant("loading the tenant dashboard", "   ")
	if err == nil {
		t.Fatal("expected an error for a blank tenant")
	}
	if !errors.Is(err, ErrTenantNotGiven) {
		t.Fatalf("expected ErrTenantNotGiven, got: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "loading the tenant dashboard") {
		t.Fatalf("error must name the operation: %q", msg)
	}
	if !strings.Contains(msg, "reopen the tab or dialog") {
		t.Fatalf("error must name its recovery: %q", msg)
	}
}
