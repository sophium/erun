package eruncommon

import (
	"errors"
	"strings"
	"testing"
)

// openTenantResolutionStore is a minimal OpenStore for exercising
// resolveOpenTenant/resolveOpenEnvironment without a real config root.
type openTenantResolutionStore struct {
	defaultTenant      string
	defaultEnvironment string
}

func (s openTenantResolutionStore) LoadERunConfig() (ERunConfig, string, error) {
	if s.defaultTenant == "" {
		return ERunConfig{}, "", ErrNotInitialized
	}
	return ERunConfig{DefaultTenant: s.defaultTenant}, "", nil
}

func (s openTenantResolutionStore) LoadTenantConfig(name string) (TenantConfig, string, error) {
	return TenantConfig{Name: name, DefaultEnvironment: s.defaultEnvironment}, "", nil
}

func (s openTenantResolutionStore) LoadEnvConfig(tenant, environment string) (EnvConfig, string, error) {
	return EnvConfig{Name: environment}, "", nil
}

// noProjectRoot simulates running outside any tenant's project checkout, so
// resolveOpenTenant's cwd fallback never resolves a tenant.
func noProjectRoot() (string, string, error) {
	return "", "", ErrNotInGitRepository
}

func TestResolveOpenTenantInferenceForbidden(t *testing.T) {
	store := openTenantResolutionStore{}
	_, err := resolveOpenTenant(store, noProjectRoot, OpenParams{UseDefaultTenant: false})
	if err == nil {
		t.Fatal("expected an error when no tenant is given and inference is not permitted")
	}
	if !errors.Is(err, ErrOpenTenantNotProvided) {
		t.Fatalf("expected ErrOpenTenantNotProvided, got: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "open") {
		t.Fatalf("error must name the operation (open): %q", msg)
	}
	if !strings.Contains(msg, "pass a tenant explicitly") {
		t.Fatalf("error must name its recovery (pass a tenant explicitly): %q", msg)
	}
	if errors.Is(err, ErrDefaultTenantNotConfigured) {
		t.Fatalf("inference-forbidden case must not be mistaken for the inference-permitted-but-unresolved case: %q", msg)
	}
}

func TestResolveOpenTenantInferencePermittedButUnresolved(t *testing.T) {
	store := openTenantResolutionStore{}
	_, err := resolveOpenTenant(store, noProjectRoot, OpenParams{UseDefaultTenant: true})
	if err == nil {
		t.Fatal("expected an error when no tenant is given and none could be inferred")
	}
	if !errors.Is(err, ErrDefaultTenantNotConfigured) {
		t.Fatalf("expected ErrDefaultTenantNotConfigured, got: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "open") {
		t.Fatalf("error must name the operation (open): %q", msg)
	}
	if !strings.Contains(msg, "pass a tenant explicitly") || !strings.Contains(msg, "set-default-tenant") {
		t.Fatalf("error must name its recovery (pass explicitly, or set a default): %q", msg)
	}
	if errors.Is(err, ErrOpenTenantNotProvided) {
		t.Fatalf("inference-permitted-but-unresolved case must not be mistaken for the inference-forbidden case: %q", msg)
	}
}

func TestResolveOpenTenantSucceedsWhenProvided(t *testing.T) {
	store := openTenantResolutionStore{}
	tenant, err := resolveOpenTenant(store, noProjectRoot, OpenParams{Tenant: "acme"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tenant != "acme" {
		t.Fatalf("expected acme, got %q", tenant)
	}
}

func TestResolveOpenTenantSucceedsFromDefault(t *testing.T) {
	store := openTenantResolutionStore{defaultTenant: "acme"}
	tenant, err := resolveOpenTenant(store, noProjectRoot, OpenParams{UseDefaultTenant: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tenant != "acme" {
		t.Fatalf("expected acme, got %q", tenant)
	}
}

func TestResolveOpenEnvironmentInferenceForbidden(t *testing.T) {
	_, err := resolveOpenEnvironment(OpenParams{UseDefaultEnvironment: false}, TenantConfig{Name: "acme"})
	if err == nil {
		t.Fatal("expected an error when no environment is given and inference is not permitted")
	}
	if !errors.Is(err, ErrOpenEnvironmentNotProvided) {
		t.Fatalf("expected ErrOpenEnvironmentNotProvided, got: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "open") {
		t.Fatalf("error must name the operation (open): %q", msg)
	}
	if !strings.Contains(msg, "pass an environment explicitly") {
		t.Fatalf("error must name its recovery: %q", msg)
	}
}

func TestResolveOpenEnvironmentInferencePermittedButUnresolved(t *testing.T) {
	_, err := resolveOpenEnvironment(OpenParams{UseDefaultEnvironment: true}, TenantConfig{Name: "acme"})
	if err == nil {
		t.Fatal("expected an error when no environment is given and none could be inferred")
	}
	if !errors.Is(err, ErrDefaultEnvironmentNotConfigured) {
		t.Fatalf("expected ErrDefaultEnvironmentNotConfigured, got: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "acme") {
		t.Fatalf("error must name the tenant it could not infer an environment for: %q", msg)
	}
	if !strings.Contains(msg, "pass an environment explicitly") {
		t.Fatalf("error must name its recovery: %q", msg)
	}
	if errors.Is(err, ErrOpenEnvironmentNotProvided) {
		t.Fatalf("inference-permitted-but-unresolved case must not be mistaken for the inference-forbidden case: %q", msg)
	}
}
