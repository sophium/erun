package eruncommon

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRunDeleteEnvironmentDeletesRemoteNamespaceAndConfig(t *testing.T) {
	setupConfigTestXDGConfigHome(t)

	if err := SaveTenantConfig(TenantConfig{Name: "tenant-a", DefaultEnvironment: "dev"}); err != nil {
		t.Fatalf("SaveTenantConfig failed: %v", err)
	}
	if err := SaveEnvConfig("tenant-a", EnvConfig{
		Name:              "dev",
		RepoPath:          "/home/erun/git/tenant-a",
		KubernetesContext: "cluster-dev",
		Remote:            true,
	}); err != nil {
		t.Fatalf("SaveEnvConfig failed: %v", err)
	}

	var deletedContext string
	var deletedNamespace string
	result, err := RunDeleteEnvironment(Context{}, DeleteEnvironmentParams{Tenant: "tenant-a", Environment: "dev"}, ConfigStore{}, func(contextName, namespace string) error {
		deletedContext = contextName
		deletedNamespace = namespace
		return nil
	})
	if err != nil {
		t.Fatalf("RunDeleteEnvironment failed: %v", err)
	}

	if deletedContext != "cluster-dev" || deletedNamespace != "tenant-a-dev" {
		t.Fatalf("unexpected namespace deletion: context=%q namespace=%q", deletedContext, deletedNamespace)
	}
	if result.Namespace != "tenant-a-dev" || !result.Remote {
		t.Fatalf("unexpected result: %+v", result)
	}
	if _, _, err := LoadEnvConfig("tenant-a", "dev"); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("expected env config to be deleted, got %v", err)
	}
	if _, _, err := LoadTenantConfig("tenant-a"); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("expected tenant config to be deleted, got %v", err)
	}
}

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

func TestRunDeleteEnvironmentDryRunTracesWithoutDeleting(t *testing.T) {
	setupConfigTestXDGConfigHome(t)

	if err := SaveTenantConfig(TenantConfig{Name: "tenant-a", DefaultEnvironment: "dev"}); err != nil {
		t.Fatalf("SaveTenantConfig failed: %v", err)
	}
	if err := SaveEnvConfig("tenant-a", EnvConfig{Name: "dev", KubernetesContext: "cluster-dev", Remote: true}); err != nil {
		t.Fatalf("SaveEnvConfig failed: %v", err)
	}

	stderr := new(bytes.Buffer)
	called := false
	_, err := RunDeleteEnvironment(Context{
		Logger: NewLoggerWithWriters(2, stderr, stderr),
		DryRun: true,
	}, DeleteEnvironmentParams{Tenant: "tenant-a", Environment: "dev"}, ConfigStore{}, func(string, string) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("RunDeleteEnvironment failed: %v", err)
	}
	if called {
		t.Fatal("did not expect namespace deletion during dry-run")
	}
	if _, _, err := LoadEnvConfig("tenant-a", "dev"); err != nil {
		t.Fatalf("expected env config to remain during dry-run, got %v", err)
	}
	output := stderr.String()
	for _, want := range []string{
		"kubectl --context cluster-dev delete namespace tenant-a-dev --ignore-not-found",
		"rm -rf",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected trace to contain %q, got %q", want, output)
		}
	}
}
