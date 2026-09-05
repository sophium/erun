package eruncommon

import "testing"

func TestClusterRegistryWithDefaults(t *testing.T) {
	got := ClusterRegistry{}.WithDefaults()
	if got.Service != DefaultClusterRegistryService {
		t.Errorf("service = %q, want %q", got.Service, DefaultClusterRegistryService)
	}
	if got.Namespace != DefaultClusterRegistryNamespace {
		t.Errorf("namespace = %q, want %q", got.Namespace, DefaultClusterRegistryNamespace)
	}
	if got.Port != DefaultClusterRegistryPort {
		t.Errorf("port = %d, want %d", got.Port, DefaultClusterRegistryPort)
	}
	// An explicit value is preserved.
	custom := ClusterRegistry{Service: "reg", Namespace: "ns", Port: 6000}.WithDefaults()
	if custom.Service != "reg" || custom.Namespace != "ns" || custom.Port != 6000 {
		t.Errorf("explicit fields not preserved: %+v", custom)
	}
}

func TestClusterContainerRegistries(t *testing.T) {
	list := ClusterContainerRegistries(ClusterRegistry{Insecure: true})
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	entry := list[0]
	if entry.Registry != "" {
		t.Errorf("registry = %q, want empty (cluster entry)", entry.Registry)
	}
	if entry.Cluster == nil {
		t.Fatal("cluster is nil, want a cluster block")
	}
	if entry.Cluster.Service != DefaultClusterRegistryService ||
		entry.Cluster.Namespace != DefaultClusterRegistryNamespace ||
		entry.Cluster.Port != DefaultClusterRegistryPort {
		t.Errorf("cluster defaults not filled: %+v", *entry.Cluster)
	}
	if !entry.Cluster.Insecure {
		t.Error("insecure not preserved")
	}
}

func TestClusterContainerRegistriesRolesAndValidate(t *testing.T) {
	list := ClusterContainerRegistries(ClusterRegistry{Insecure: true})
	entry := list[0]
	if !entry.hasRole(RegistryRoleBuild) || !entry.hasRole(RegistryRoleDeploy) {
		t.Errorf("roles = %v, want build+deploy", entry.Roles)
	}
	if err := list.Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}
}

func TestContainerRegistriesValidateRejectsBothRegistryAndCluster(t *testing.T) {
	list := ContainerRegistries{{
		Registry: "ghcr.io/sophium",
		Cluster:  &ClusterRegistry{},
		Roles:    []RegistryRole{RegistryRoleBuild, RegistryRoleDeploy},
	}}
	if err := list.Validate(); err == nil {
		t.Fatal("expected error when both registry and cluster are set")
	}
}

func TestContainerRegistriesConcreteExpandsClusterEntry(t *testing.T) {
	list := ContainerRegistries{
		{Cluster: &ClusterRegistry{}, Roles: []RegistryRole{RegistryRoleBuild, RegistryRoleDeploy, RegistryRoleFrom}},
		{Registry: "ghcr.io/sophium", Roles: []RegistryRole{RegistryRoleTo}},
	}
	resolve := func(c ClusterRegistry) (ClusterRegistryAddresses, error) {
		return ClusterRegistryAddresses{Push: "localhost:41000", Pull: "10.43.0.50:5000"}, nil
	}
	concrete, err := list.Concrete(resolve)
	if err != nil {
		t.Fatalf("Concrete: %v", err)
	}
	if got, _ := concrete.BuildRegistry(); got != "localhost:41000" {
		t.Errorf("build registry = %q, want push address", got)
	}
	if got, _ := concrete.FromRegistry(); got != "localhost:41000" {
		t.Errorf("from registry = %q, want push address", got)
	}
	if got, _ := concrete.DeployRegistry(); got != "10.43.0.50:5000" {
		t.Errorf("deploy registry = %q, want pull address", got)
	}
	if got := concrete.ToRegistries(); len(got) != 1 || got[0] != "ghcr.io/sophium" {
		t.Errorf("to registries = %v, want [ghcr.io/sophium]", got)
	}
	if concrete.HasClusterEntry() {
		t.Error("concrete list should carry no cluster entries")
	}
}

// TestContainerRegistriesConcretePreservesInsecure locks the fix for a cluster
// registry marked `insecure: true`: expandClusterEntry must carry that marker
// onto the concretized BUILD entry, since `docker manifest` (unlike the
// daemon) never reads --insecure-registry and needs the flag passed
// explicitly by anything that resolves a build registry.
func TestContainerRegistriesConcretePreservesInsecure(t *testing.T) {
	list := ContainerRegistries{
		{Cluster: &ClusterRegistry{Insecure: true}, Roles: []RegistryRole{RegistryRoleBuild, RegistryRoleDeploy}},
	}
	resolve := func(c ClusterRegistry) (ClusterRegistryAddresses, error) {
		return ClusterRegistryAddresses{Push: "localhost:41000", Pull: "10.43.0.50:5000"}, nil
	}
	concrete, err := list.Concrete(resolve)
	if err != nil {
		t.Fatalf("Concrete: %v", err)
	}
	if !concrete.BuildRegistryInsecure() {
		t.Error("BuildRegistryInsecure() = false, want true for an insecure cluster registry")
	}
}

func TestContainerRegistriesBuildRegistryInsecureFalseForPlainRegistry(t *testing.T) {
	list := DefaultContainerRegistries()
	if list.BuildRegistryInsecure() {
		t.Error("BuildRegistryInsecure() = true for a plain registry, want false")
	}
}

func TestContainerRegistriesConcreteRequiresResolver(t *testing.T) {
	list := ContainerRegistries{{Cluster: &ClusterRegistry{}, Roles: []RegistryRole{RegistryRoleBuild, RegistryRoleDeploy}}}
	if _, err := list.Concrete(nil); err == nil {
		t.Fatal("expected error when resolver is nil for a cluster entry")
	}
}

func TestContainerRegistriesConcretePassesThroughPlainList(t *testing.T) {
	list := DefaultContainerRegistries()
	concrete, err := list.Concrete(nil)
	if err != nil {
		t.Fatalf("Concrete on plain list: %v", err)
	}
	if !concrete.Equal(list) {
		t.Errorf("plain list changed by Concrete: %v", concrete)
	}
}
