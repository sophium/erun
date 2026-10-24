package main

import (
	"context"
	"net"
	"reflect"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	deploypkg "github.com/sophium/erun/erun-common/primitive/deploy"
)

func TestLoadAndSaveEnvironmentConfig(t *testing.T) {
	projectRoot := t.TempDir()
	rootConfig := &configpkg.ERunConfig{
		CloudProviders: []model.CloudProviderConfig{
			{Alias: "team-cloud", Provider: configpkg.CloudProviderAWS},
		},
		CloudContexts: []model.CloudContextConfig{
			{
				Name:               "team-context",
				Provider:           configpkg.CloudProviderAWS,
				CloudProviderAlias: "team-cloud",
				Region:             cloud.DefaultCloudContextRegion,
				InstanceID:         "i-test",
				InstanceType:       configpkg.DefaultCloudContextInstanceType,
				DiskType:           configpkg.DefaultCloudContextDiskType,
				DiskSizeGB:         configpkg.DefaultCloudContextDiskSizeGB,
				KubernetesContext:  "cluster-old",
			},
		},
	}
	store := stubUIStore{
		config: rootConfig,
		projectConfigs: map[string]configpkg.ProjectConfig{
			projectRoot: {ContainerRegistries: configpkg.SingleContainerRegistries("registry.example/project")},
		},
		tenants: map[string]model.TenantConfig{
			"frs": {
				Name: "frs",
			},
		},
		envs: map[string]configpkg.EnvConfig{
			"frs/prod": {
				Name:                "prod",
				LocalRepoPath:       projectRoot,
				KubernetesContext:   "cluster-old",
				ContainerRegistries: configpkg.SingleContainerRegistries("registry.example/old"),
				CloudProviderAlias:  "team-cloud",
				RuntimeVersion:      "1.0.0",
				RuntimePod: model.RuntimePodResources{
					CPU:    "4",
					Memory: "8916Mi",
				},
				SSHD: configpkg.SSHDConfig{
					Enabled:       false,
					LocalPort:     60022,
					PublicKeyPath: "/tmp/old.pub",
				},
				Type: configpkg.EnvironmentTypeLocalAgent,
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})
	// Cloud-context Status is not persisted; seed the in-memory cache instead.
	app.setCloudContextStatusInCache("team-context", cloud.CloudContextStatusStopped)

	loaded, err := app.LoadEnvironmentConfig(uiSelection{Tenant: " frs ", Environment: " prod "})
	if err != nil {
		t.Fatalf("LoadEnvironmentConfig failed: %v", err)
	}
	assertLoadedEnvironmentConfig(t, loaded, projectRoot)

	saved, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "prod"}, uiEnvironmentConfig{
		Name:              "prod",
		RepoPath:          " /tmp/repo ",
		KubernetesContext: " cluster-new ",
		ContainerRegistries: []uiContainerRegistryEntry{
			{Registry: " registry.example/team ", Roles: []string{"build", "deploy"}},
		},
		CloudProviderAlias: " other-cloud ",
		RuntimeVersion:     " 1.2.3 ",
		RuntimePod: uiRuntimePodConfig{
			CPU:    "6",
			Memory: "12Gi",
		},
		SSHD: uiSSHDConfig{
			Enabled:       true,
			LocalPort:     62222,
			PublicKeyPath: " /tmp/id_ed25519.pub ",
		},
		Type: configpkg.EnvironmentTypeRuntime,
	})
	if err != nil {
		t.Fatalf("SaveEnvironmentConfig failed: %v", err)
	}
	assertSavedEnvironmentConfig(t, saved, projectRoot)
	stored := store.envs["frs/prod"]
	assertStoredEnvironmentConfig(t, stored, projectRoot)
}

// Covers the non-empty selection path the headless Playwright baseline cannot reach.
func TestSaveAndLoadDeployComponentsRoundTrip(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config:  &configpkg.ERunConfig{},
		tenants: map[string]model.TenantConfig{"frs": {Name: "frs"}},
		envs: map[string]configpkg.EnvConfig{
			"frs/prod": {
				Name:           "prod",
				LocalRepoPath:  projectRoot,
				RuntimeVersion: "1.0.0",
				Type:           configpkg.EnvironmentTypeLocalAgent,
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	saved, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "prod"}, uiEnvironmentConfig{
		Name:             "prod",
		RepoPath:         projectRoot,
		DeployComponents: []string{"frs-backend-postgres", " ", "frs-backend-api"},
	})
	if err != nil {
		t.Fatalf("SaveEnvironmentConfig failed: %v", err)
	}
	want := []string{"frs-backend-postgres", "frs-backend-api"}
	if !reflect.DeepEqual(saved.DeployComponents, want) {
		t.Fatalf("saved DeployComponents = %v, want %v (trimmed)", saved.DeployComponents, want)
	}
	if got := store.envs["frs/prod"].Deploy.Components; !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted deploy.components = %v, want %v", got, want)
	}
	loaded, err := app.LoadEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "prod"})
	if err != nil {
		t.Fatalf("LoadEnvironmentConfig failed: %v", err)
	}
	if !reflect.DeepEqual(loaded.DeployComponents, want) {
		t.Fatalf("loaded DeployComponents = %v, want %v", loaded.DeployComponents, want)
	}
}

// A local-agent env lists the charts published at the deploy version plus the
// runtime, never its working-tree chart directories.
func TestLoadDeployComponentsLocalAgentShowsPublishedVersionView(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config:  &configpkg.ERunConfig{},
		tenants: map[string]model.TenantConfig{"frs": {Name: "frs"}},
		envs: map[string]configpkg.EnvConfig{
			"frs/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "test-context",
				RuntimeVersion:    "1.0.106",
				Type:              configpkg.EnvironmentTypeLocalAgent,
			},
		},
	}
	chartTags := map[string][]string{
		"charts/frs-backend-postgres": {"1.0.0"},
		"charts/frs-backend-db":       {"1.0.0"},
		"charts/frs-backend-api":      {"1.0.0"},
		"charts/frs-powerdns":         {"1.0.0"},
		"charts/frs-docs":             {"1.0.0"},
	}
	app := NewApp(erunUIDeps{
		store: store,
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (model.RuntimeRegistryVersions, error) {
			if namespace != configpkg.DefaultContainerRegistry {
				t.Fatalf("unexpected chart registry namespace: %s", namespace)
			}
			return model.RuntimeRegistryVersions{Tags: chartTags[repository]}, nil
		},
	})

	names := func(components []deploypkg.DeployableComponent) []string {
		out := make([]string, 0, len(components))
		for _, component := range components {
			out = append(out, component.Name)
		}
		return out
	}

	at100, err := app.LoadDeployComponents(uiSelection{Tenant: "frs", Environment: "local", Version: "1.0.0"})
	if err != nil {
		t.Fatalf("LoadDeployComponents(1.0.0) failed: %v", err)
	}
	wantAt100 := []string{
		"frs-devops", "frs-backend-postgres", "frs-backend-db",
		"frs-backend-api", "frs-powerdns", "frs-docs",
	}
	if got := names(at100.Components); !reflect.DeepEqual(got, wantAt100) {
		t.Fatalf("components at 1.0.0 = %v, want %v (published-version view, runtime first)", got, wantAt100)
	}
	runtime := at100.Components[0]
	if !runtime.Runtime || !runtime.Selected || runtime.Source != "published-chart" {
		t.Fatalf("runtime item = %+v, want {frs-devops runtime selected published-chart}", runtime)
	}

	// No version-to-deploy: the current version (1.0.106) publishes no component charts.
	atCurrent, err := app.LoadDeployComponents(uiSelection{Tenant: "frs", Environment: "local"})
	if err != nil {
		t.Fatalf("LoadDeployComponents(current) failed: %v", err)
	}
	if got, want := names(atCurrent.Components), []string{"frs-devops"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("components at current 1.0.106 = %v, want %v (runtime only)", got, want)
	}
}

// The registry probe cannot be driven from headless Playwright, so filtering is asserted here.
func TestLoadDeployComponentsVersionAwareFiltersUnavailableCharts(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config:  &configpkg.ERunConfig{},
		tenants: map[string]model.TenantConfig{"frs": {Name: "frs"}},
		envs: map[string]configpkg.EnvConfig{
			"frs/prod": {
				Name:              "prod",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "test-context",
				RuntimeVersion:    "1.0.106",
				Type:              configpkg.EnvironmentTypeRuntime,
			},
		},
	}
	chartTags := map[string][]string{
		"charts/frs-backend-postgres": {"1.0.112"},
		"charts/frs-backend-api":      {"1.0.112"},
	}
	app := NewApp(erunUIDeps{
		store: store,
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (model.RuntimeRegistryVersions, error) {
			if namespace != configpkg.DefaultContainerRegistry {
				t.Fatalf("unexpected chart registry namespace: %s", namespace)
			}
			return model.RuntimeRegistryVersions{Tags: chartTags[repository]}, nil
		},
	})

	names := func(components []deploypkg.DeployableComponent) []string {
		out := make([]string, 0, len(components))
		for _, component := range components {
			out = append(out, component.Name)
		}
		return out
	}

	at112, err := app.LoadDeployComponents(uiSelection{Tenant: "frs", Environment: "prod", Version: "1.0.112"})
	if err != nil {
		t.Fatalf("LoadDeployComponents(1.0.112) failed: %v", err)
	}
	if got, want := names(at112.Components), []string{"frs-devops", "frs-backend-postgres", "frs-backend-api"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("components at 1.0.112 = %v, want %v (runtime first; unpublished db/powerdns/docs filtered out)", got, want)
	}

	atCurrent, err := app.LoadDeployComponents(uiSelection{Tenant: "frs", Environment: "prod"})
	if err != nil {
		t.Fatalf("LoadDeployComponents(current) failed: %v", err)
	}
	if got, want := names(atCurrent.Components), []string{"frs-devops"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("components at current version 1.0.106 = %v, want %v (runtime only)", got, want)
	}
}

// The runtime row must name the chart resolvePublishedRuntimeChartReference installs.
func TestLoadDeployComponentsRuntimeChartReflectsTenantChart(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config:  &configpkg.ERunConfig{},
		tenants: map[string]model.TenantConfig{"frs": {Name: "frs"}},
		envs: map[string]configpkg.EnvConfig{
			"frs/prod": {
				Name:              "prod",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "test-context",
				RuntimeVersion:    "1.0.20",
				Type:              configpkg.EnvironmentTypeRuntime,
			},
		},
	}
	runtimePublishedChart := func(t *testing.T, tags map[string][]string) string {
		t.Helper()
		app := NewApp(erunUIDeps{
			store: store,
			resolveImageRegistry: func(_ context.Context, _, repository string) (model.RuntimeRegistryVersions, error) {
				return model.RuntimeRegistryVersions{Tags: tags[repository]}, nil
			},
		})
		components, err := app.LoadDeployComponents(uiSelection{Tenant: "frs", Environment: "prod", Version: "1.0.20"})
		if err != nil {
			t.Fatalf("LoadDeployComponents failed: %v", err)
		}
		for _, component := range components.Components {
			if component.Runtime {
				return component.PublishedChart
			}
		}
		t.Fatal("no runtime component returned")
		return ""
	}

	if got := runtimePublishedChart(t, map[string][]string{"charts/frs-devops": {"1.0.20"}}); got != "frs-devops" {
		t.Fatalf("runtime PublishedChart with tenant chart published = %q, want frs-devops", got)
	}
	if got := runtimePublishedChart(t, map[string][]string{}); got != "erun-devops" {
		t.Fatalf("runtime PublishedChart with no tenant chart = %q, want erun-devops (fallback)", got)
	}
}

func assertLoadedEnvironmentConfig(t *testing.T, loaded uiEnvironmentConfig, projectRoot string) {
	t.Helper()

	if loaded.Name != "prod" || loaded.RepoPath != projectRoot || loaded.KubernetesContext != "cluster-old" {
		t.Fatalf("unexpected loaded config: %+v", loaded)
	}
	if loaded.CloudContext == nil || loaded.CloudContext.Name != "team-context" || loaded.CloudContext.Status != cloud.CloudContextStatusStopped {
		t.Fatalf("expected linked cloud context, got %+v", loaded.CloudContext)
	}
	if loaded.RuntimePod.CPU != "4" || loaded.RuntimePod.Memory != "8916Mi" {
		t.Fatalf("unexpected loaded runtime pod config: %+v", loaded.RuntimePod)
	}
	assertLocalPorts(t, loaded.LocalPorts)
}

func assertSavedEnvironmentConfig(t *testing.T, saved uiEnvironmentConfig, projectRoot string) {
	t.Helper()

	if saved.RepoPath != projectRoot || saved.KubernetesContext != "cluster-old" || uiRegistryWithRole(saved.ContainerRegistries, "build") != "registry.example/team" || saved.RuntimeVersion != "1.0.0" || saved.CloudProviderAlias != "other-cloud" {
		t.Fatalf("unexpected saved config: %+v", saved)
	}
	if saved.RuntimePod.CPU != "6" || saved.RuntimePod.Memory != "12Gi" {
		t.Fatalf("unexpected saved runtime pod config: %+v", saved.RuntimePod)
	}
	assertLocalPorts(t, saved.LocalPorts)
}

func uiRegistryWithRole(entries []uiContainerRegistryEntry, role string) string {
	for _, entry := range entries {
		for _, candidate := range entry.Roles {
			if candidate == role {
				return entry.Registry
			}
		}
	}
	return ""
}

func assertLocalPorts(t *testing.T, ports uiEnvironmentLocalPorts) {
	t.Helper()

	if ports.RangeStart != 17000 || ports.RangeEnd != 17099 || ports.MCP != 17000 || ports.API != 17033 || ports.SSH != 60022 {
		t.Fatalf("unexpected local ports: %+v", ports)
	}
}

func assertStoredEnvironmentConfig(t *testing.T, stored configpkg.EnvConfig, projectRoot string) {
	t.Helper()

	storedRegistry, _ := stored.ContainerRegistries.BuildRegistry()
	if stored.EffectiveLocalRepoPath() != projectRoot || stored.Type != configpkg.EnvironmentTypeRuntime || stored.RuntimeVersion != "1.0.0" || storedRegistry != "registry.example/team" || stored.CloudProviderAlias != "other-cloud" {
		t.Fatalf("unexpected stored config: %+v", stored)
	}
	assertStoredEnvironmentSSHD(t, stored)
	if stored.RuntimePod.CPU != "6" || stored.RuntimePod.Memory != "12Gi" {
		t.Fatalf("unexpected stored runtime pod config: %+v", stored.RuntimePod)
	}
}

func assertStoredEnvironmentSSHD(t *testing.T, stored configpkg.EnvConfig) {
	t.Helper()

	if stored.SSHD.Enabled || stored.SSHD.LocalPort != 60022 || stored.SSHD.PublicKeyPath != "/tmp/old.pub" {
		t.Fatalf("unexpected stored config: %+v", stored)
	}
}

func TestLocalPortStatusReportsAvailability(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on ephemeral port: %v", err)
	}
	port := tcpListenerPort(t, listener)

	connectable := localPortStatus(port)
	if !connectable.Available || connectable.Status != "Yes" {
		t.Fatalf("expected connectable status, got %+v", connectable)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	unreachable := localPortStatus(port)
	if unreachable.Available || unreachable.Status != "No" {
		t.Fatalf("expected unreachable status, got %+v", unreachable)
	}
}
