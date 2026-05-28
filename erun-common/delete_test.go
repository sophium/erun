package eruncommon

import (
	"errors"
	"testing"
)

func TestRunDeleteEnvironmentKeepsTenantWhenOtherEnvironmentsRemain(t *testing.T) {
	setupConfigTestXDGConfigHome(t)

	if err := SaveTenantConfig(TenantConfig{Name: "tenant-a", DefaultEnvironment: "dev"}); err != nil {
		t.Fatalf("SaveTenantConfig failed: %v", err)
	}
	if err := SaveEnvConfig("tenant-a", EnvConfig{Name: "dev"}); err != nil {
		t.Fatalf("SaveEnvConfig failed: %v", err)
	}
	if err := SaveEnvConfig("tenant-a", EnvConfig{Name: "prod"}); err != nil {
		t.Fatalf("SaveEnvConfig failed: %v", err)
	}

	if _, err := RunDeleteEnvironment(Context{}, DeleteEnvironmentParams{Tenant: "tenant-a", Environment: "dev"}, ConfigStore{}, nil); err != nil {
		t.Fatalf("RunDeleteEnvironment failed: %v", err)
	}

	tenantConfig, _, err := LoadTenantConfig("tenant-a")
	if err != nil {
		t.Fatalf("LoadTenantConfig failed: %v", err)
	}
	if tenantConfig.DefaultEnvironment != "prod" {
		t.Fatalf("expected remaining environment to become default, got %+v", tenantConfig)
	}
	if _, _, err := LoadEnvConfig("tenant-a", "prod"); err != nil {
		t.Fatalf("expected remaining env config, got %v", err)
	}
}

func TestRunDeleteEnvironmentDeletesConfigWhenNamespaceDeleteFails(t *testing.T) {
	setupConfigTestXDGConfigHome(t)

	if err := SaveTenantConfig(TenantConfig{Name: "tenant-a", DefaultEnvironment: "dev"}); err != nil {
		t.Fatalf("SaveTenantConfig failed: %v", err)
	}
	if err := SaveEnvConfig("tenant-a", EnvConfig{Name: "dev", KubernetesContext: "cluster-dev", Remote: true}); err != nil {
		t.Fatalf("SaveEnvConfig failed: %v", err)
	}

	result, err := RunDeleteEnvironment(Context{}, DeleteEnvironmentParams{Tenant: "tenant-a", Environment: "dev"}, ConfigStore{}, func(string, string) error {
		return errors.New("api unavailable")
	})
	if err != nil {
		t.Fatalf("RunDeleteEnvironment failed: %v", err)
	}
	if result.NamespaceDeleteError != "api unavailable" {
		t.Fatalf("expected namespace delete warning, got %+v", result)
	}
	if _, _, err := LoadEnvConfig("tenant-a", "dev"); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("expected env config to be deleted, got %v", err)
	}
}

