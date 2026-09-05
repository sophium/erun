package main

import (
	"errors"
	"strings"
	"testing"
)

func TestLoadTenantConfigRequiresATenant(t *testing.T) {
	app := NewApp(erunUIDeps{store: stubUIStore{}})
	_, err := app.LoadTenantConfig("   ")
	if err == nil || !errors.Is(err, ErrTenantNotGiven) {
		t.Fatalf("expected ErrTenantNotGiven, got %v", err)
	}
	if !strings.Contains(err.Error(), "loading tenant settings") {
		t.Fatalf("expected the error to name its operation, got %v", err)
	}
}

func TestSaveTenantConfigRequiresATenant(t *testing.T) {
	app := NewApp(erunUIDeps{store: stubUIStore{}})
	_, err := app.SaveTenantConfig(uiTenantConfig{Name: "  "})
	if err == nil || !errors.Is(err, ErrTenantNotGiven) {
		t.Fatalf("expected ErrTenantNotGiven, got %v", err)
	}
	if !strings.Contains(err.Error(), "saving tenant settings") {
		t.Fatalf("expected the error to name its operation, got %v", err)
	}
}
