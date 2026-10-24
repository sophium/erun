package main

import (
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

func TestLoadEnvironmentConfigUsesProjectContainerRegistryForAllEnvironments(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		projectConfigs: map[string]configpkg.ProjectConfig{
			projectRoot: {
				ContainerRegistries: configpkg.SingleContainerRegistries("registry.example/shared"),
				Environments: map[string]configpkg.ProjectEnvironmentConfig{
					"prod": {ContainerRegistries: configpkg.SingleContainerRegistries("registry.example/prod")},
				},
			},
		},
		tenants: map[string]configpkg.TenantConfig{
			"frs": {
				Name: "frs",
			},
		},
		envs: map[string]configpkg.EnvConfig{
			"frs/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-local",
			},
			"frs/prod": {
				Name:              "prod",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-prod",
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	local, err := app.LoadEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "local"})
	if err != nil {
		t.Fatalf("LoadEnvironmentConfig local failed: %v", err)
	}
	if uiRegistryWithRole(local.ContainerRegistries, "build") != "registry.example/shared" {
		t.Fatalf("expected local env to use project-wide registry, got %+v", local)
	}

	prod, err := app.LoadEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "prod"})
	if err != nil {
		t.Fatalf("LoadEnvironmentConfig prod failed: %v", err)
	}
	if uiRegistryWithRole(prod.ContainerRegistries, "build") != "registry.example/prod" {
		t.Fatalf("expected prod env to use environment registry override, got %+v", prod)
	}
}

func TestSaveEnvironmentConfigPreservesProjectContainerRegistryReadModel(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		projectConfigs: map[string]configpkg.ProjectConfig{
			projectRoot: {ContainerRegistries: configpkg.SingleContainerRegistries("registry.example/shared")},
		},
		tenants: map[string]configpkg.TenantConfig{
			"frs": {
				Name: "frs",
			},
		},
		envs: map[string]configpkg.EnvConfig{
			"frs/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-local",
				RuntimePod: configpkg.RuntimePodResources{
					CPU:    "4",
					Memory: "8916Mi",
				},
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	saved, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "local"}, uiEnvironmentConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
		RuntimePod: uiRuntimePodConfig{
			CPU:    "6",
			Memory: "12Gi",
		},
		Idle: uiIdleConfig{
			Timeout:      configpkg.DefaultEnvironmentIdleTimeout.String(),
			WorkingHours: configpkg.DefaultEnvironmentWorkingHours,
		},
	})
	if err != nil {
		t.Fatalf("SaveEnvironmentConfig failed: %v", err)
	}
	if uiRegistryWithRole(saved.ContainerRegistries, "build") != "registry.example/shared" {
		t.Fatalf("expected saved UI config to keep effective project registry, got %+v", saved)
	}
	stored := store.envs["frs/local"]
	if len(stored.ContainerRegistries) != 0 {
		t.Fatalf("expected env save not to copy project registry into env config, got %+v", stored)
	}
}

// Local-agent registry lists live in the project config, where build/deploy read them.
func TestSaveEnvironmentConfigWritesLocalAgentRegistryListToProjectConfig(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		projectConfigs: map[string]configpkg.ProjectConfig{
			projectRoot: {ContainerRegistries: configpkg.SingleContainerRegistries("ghcr.io/acme")},
		},
		tenants: map[string]configpkg.TenantConfig{"frs": {Name: "frs"}},
		envs: map[string]configpkg.EnvConfig{
			"frs/local": {Name: "local", LocalRepoPath: projectRoot, KubernetesContext: "c", Type: configpkg.EnvironmentTypeLocalAgent},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	saved, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "local"}, uiEnvironmentConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "c",
		Type:              configpkg.EnvironmentTypeLocalAgent,
		ContainerRegistries: []uiContainerRegistryEntry{
			{Registry: "ghcr.io/acme", Roles: []string{"build", "from"}},
			{Registry: "registry.internal/acme", Roles: []string{"to", "deploy"}},
		},
		Idle: uiIdleConfig{Timeout: configpkg.DefaultEnvironmentIdleTimeout.String(), WorkingHours: configpkg.DefaultEnvironmentWorkingHours},
	})
	if err != nil {
		t.Fatalf("SaveEnvironmentConfig: %v", err)
	}
	if len(store.envs["frs/local"].ContainerRegistries) != 0 {
		t.Fatalf("a local-agent env must not carry the list on env config, got %+v", store.envs["frs/local"].ContainerRegistries)
	}
	list := store.projectConfigs[projectRoot].ContainerRegistriesForEnvironment("local")
	if from, ok := list.FromRegistry(); !ok || from != "ghcr.io/acme" {
		t.Fatalf("expected from=ghcr.io/acme in project config, got %+v", list)
	}
	if deploy, ok := list.DeployRegistry(); !ok || deploy != "registry.internal/acme" {
		t.Fatalf("expected deploy=registry.internal/acme in project config, got %+v", list)
	}
	if uiRegistryWithRole(saved.ContainerRegistries, "deploy") != "registry.internal/acme" {
		t.Fatalf("saved UI must reflect the edited list, got %+v", saved.ContainerRegistries)
	}
}

func TestSaveEnvironmentConfigRejectsInvalidRegistryList(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]configpkg.TenantConfig{"frs": {Name: "frs"}},
		envs: map[string]configpkg.EnvConfig{
			"frs/local": {Name: "local", LocalRepoPath: projectRoot, Type: configpkg.EnvironmentTypeLocalAgent},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	_, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "local"}, uiEnvironmentConfig{
		Name:     "local",
		RepoPath: projectRoot,
		Type:     configpkg.EnvironmentTypeLocalAgent,
		ContainerRegistries: []uiContainerRegistryEntry{
			{Registry: "ghcr.io/acme", Roles: []string{"build"}},
		},
		Idle: uiIdleConfig{Timeout: configpkg.DefaultEnvironmentIdleTimeout.String(), WorkingHours: configpkg.DefaultEnvironmentWorkingHours},
	})
	if err == nil {
		t.Fatal("expected a validation error for a registry list with no deploy registry")
	}
}

// A deploy-only registry may serve externally published images, so it is valid.
func TestSaveEnvironmentConfigAcceptsDeployOnlyRegistry(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]configpkg.TenantConfig{"frs": {Name: "frs"}},
		envs: map[string]configpkg.EnvConfig{
			"frs/prod": {Name: "prod", LocalRepoPath: projectRoot, Type: configpkg.EnvironmentTypeRuntime},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	saved, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "prod"}, uiEnvironmentConfig{
		Name: "prod",
		Type: configpkg.EnvironmentTypeRuntime,
		ContainerRegistries: []uiContainerRegistryEntry{
			{Registry: "ghcr.io/sophium", Roles: []string{"deploy"}},
		},
		Idle: uiIdleConfig{Timeout: configpkg.DefaultEnvironmentIdleTimeout.String(), WorkingHours: configpkg.DefaultEnvironmentWorkingHours},
	})
	if err != nil {
		t.Fatalf("deploy-only registry must be accepted, got %v", err)
	}
	if uiRegistryWithRole(saved.ContainerRegistries, "deploy") != "ghcr.io/sophium" {
		t.Fatalf("expected the deploy-only registry to persist, got %+v", saved.ContainerRegistries)
	}
}

// Clearing the runtime chart must persist as an empty field, not be ignored.
func TestSaveEnvironmentConfigRoundTripsRuntimeChart(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]configpkg.TenantConfig{
			"frs": {Name: "frs"},
		},
		envs: map[string]configpkg.EnvConfig{
			"frs/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-local",
				RuntimeChart:      "oci://ghcr.io/sophium/charts/erun-devops:1.0.178",
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	loaded, err := app.LoadEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "local"})
	if err != nil {
		t.Fatalf("LoadEnvironmentConfig failed: %v", err)
	}
	if loaded.RuntimeChart != "oci://ghcr.io/sophium/charts/erun-devops:1.0.178" {
		t.Fatalf("expected the env's stated chart to load, got %q", loaded.RuntimeChart)
	}

	loaded.RuntimeChart = "  oci://registry.example/charts/erun-devops:1.2.3  "
	saved, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "local"}, loaded)
	if err != nil {
		t.Fatalf("SaveEnvironmentConfig failed: %v", err)
	}
	if saved.RuntimeChart != "oci://registry.example/charts/erun-devops:1.2.3" {
		t.Fatalf("expected the saved chart trimmed, got %q", saved.RuntimeChart)
	}
	if stored := store.envs["frs/local"].RuntimeChart; stored != "oci://registry.example/charts/erun-devops:1.2.3" {
		t.Fatalf("expected the env config to record the chart, got %q", stored)
	}

	saved.RuntimeChart = ""
	if _, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "local"}, saved); err != nil {
		t.Fatalf("SaveEnvironmentConfig failed clearing the chart: %v", err)
	}
	if stored := store.envs["frs/local"].RuntimeChart; stored != "" {
		t.Fatalf("expected clearing the chart to reach the env config, got %q", stored)
	}
}
