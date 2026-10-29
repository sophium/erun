package settings

import (
	"net"
	"reflect"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	configcloudcontext "github.com/sophium/erun/erun-common/base/config/cloudcontext"
	"github.com/sophium/erun/erun-common/base/model"
	cloudcloudcontext "github.com/sophium/erun/erun-common/infra/cloud/cloudcontext"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
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
				Region:             cloudcloudcontext.DefaultCloudContextRegion,
				InstanceID:         "i-test",
				InstanceType:       configcloudcontext.DefaultCloudContextInstanceType,
				DiskType:           configcloudcontext.DefaultCloudContextDiskType,
				DiskSizeGB:         configcloudcontext.DefaultCloudContextDiskSizeGB,
				KubernetesContext:  "cluster-old",
			},
		},
	}
	store := fakeStore{
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
	svc := newTestService(Deps{Store: store})
	// Cloud-context Status is not persisted; seed the in-memory cache instead.
	svc.d.CloudStatuses.SetStatus("team-context", cloudcloudcontext.CloudContextStatusStopped)

	loaded, err := svc.LoadEnvironmentConfig(uimodel.Selection{Tenant: " frs ", Environment: " prod "})
	if err != nil {
		t.Fatalf("LoadEnvironmentConfig failed: %v", err)
	}
	assertLoadedEnvironmentConfig(t, loaded, projectRoot)

	saved, err := svc.SaveEnvironmentConfig(uimodel.Selection{Tenant: "frs", Environment: "prod"}, EnvironmentConfig{
		Name:              "prod",
		RepoPath:          " /tmp/repo ",
		KubernetesContext: " cluster-new ",
		ContainerRegistries: []ContainerRegistryEntry{
			{Registry: " registry.example/team ", Roles: []string{"build", "deploy"}},
		},
		CloudProviderAlias: " other-cloud ",
		RuntimeVersion:     " 1.2.3 ",
		RuntimePod: RuntimePodConfig{
			CPU:    "6",
			Memory: "12Gi",
		},
		SSHD: SSHDConfig{
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
	store := fakeStore{
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
	svc := newTestService(Deps{Store: store})

	saved, err := svc.SaveEnvironmentConfig(uimodel.Selection{Tenant: "frs", Environment: "prod"}, EnvironmentConfig{
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
	loaded, err := svc.LoadEnvironmentConfig(uimodel.Selection{Tenant: "frs", Environment: "prod"})
	if err != nil {
		t.Fatalf("LoadEnvironmentConfig failed: %v", err)
	}
	if !reflect.DeepEqual(loaded.DeployComponents, want) {
		t.Fatalf("loaded DeployComponents = %v, want %v", loaded.DeployComponents, want)
	}
}

func assertLoadedEnvironmentConfig(t *testing.T, loaded EnvironmentConfig, projectRoot string) {
	t.Helper()

	if loaded.Name != "prod" || loaded.RepoPath != projectRoot || loaded.KubernetesContext != "cluster-old" {
		t.Fatalf("unexpected loaded config: %+v", loaded)
	}
	if loaded.CloudContext == nil || loaded.CloudContext.Name != "team-context" || loaded.CloudContext.Status != cloudcloudcontext.CloudContextStatusStopped {
		t.Fatalf("expected linked cloud context, got %+v", loaded.CloudContext)
	}
	if loaded.RuntimePod.CPU != "4" || loaded.RuntimePod.Memory != "8916Mi" {
		t.Fatalf("unexpected loaded runtime pod config: %+v", loaded.RuntimePod)
	}
	assertLocalPorts(t, loaded.LocalPorts)
}

func assertSavedEnvironmentConfig(t *testing.T, saved EnvironmentConfig, projectRoot string) {
	t.Helper()

	if saved.RepoPath != projectRoot || saved.KubernetesContext != "cluster-old" || registryWithRole(saved.ContainerRegistries, "build") != "registry.example/team" || saved.RuntimeVersion != "1.0.0" || saved.CloudProviderAlias != "other-cloud" {
		t.Fatalf("unexpected saved config: %+v", saved)
	}
	if saved.RuntimePod.CPU != "6" || saved.RuntimePod.Memory != "12Gi" {
		t.Fatalf("unexpected saved runtime pod config: %+v", saved.RuntimePod)
	}
	assertLocalPorts(t, saved.LocalPorts)
}

func assertLocalPorts(t *testing.T, ports EnvironmentLocalPorts) {
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
