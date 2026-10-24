package setup

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
)

func TestGHCRRegistriesRequiringCredentialFiltersToGHCRBuildOrDeployRoles(t *testing.T) {
	list := config.ContainerRegistries{
		{Registry: "ghcr.io/sophium", Roles: []config.RegistryRole{config.RegistryRoleBuild, config.RegistryRoleDeploy}},
		{Registry: "ghcr.io/other", Roles: []config.RegistryRole{config.RegistryRoleFrom}},
		{Registry: "020362606330.dkr.ecr.eu-west-2.amazonaws.com/acme", Roles: []config.RegistryRole{config.RegistryRoleDeploy}},
		{Cluster: &config.ClusterRegistry{Service: "erun-registry"}, Roles: []config.RegistryRole{config.RegistryRoleBuild, config.RegistryRoleDeploy}},
	}

	got := ghcrRegistriesRequiringCredential(list)
	if len(got) != 1 || got[0] != "ghcr.io/sophium" {
		t.Fatalf("got %v, want only ghcr.io/sophium (build+deploy, non-cluster, ghcr)", got)
	}
}

func TestGHCRRegistriesRequiringCredentialDedupsAndPreservesOrder(t *testing.T) {
	list := config.ContainerRegistries{
		{Registry: "ghcr.io/b", Roles: []config.RegistryRole{config.RegistryRoleDeploy}},
		{Registry: "ghcr.io/a", Roles: []config.RegistryRole{config.RegistryRoleBuild}},
		{Registry: "ghcr.io/b", Roles: []config.RegistryRole{config.RegistryRoleBuild}},
	}

	got := ghcrRegistriesRequiringCredential(list)
	want := []string{"ghcr.io/b", "ghcr.io/a"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestGHCRRegistriesRequiringCredentialEmptyForNoGHCRRegistries(t *testing.T) {
	list := config.ContainerRegistries{
		{Registry: "020362606330.dkr.ecr.eu-west-2.amazonaws.com/acme", Roles: []config.RegistryRole{config.RegistryRoleBuild, config.RegistryRoleDeploy}},
	}
	if got := ghcrRegistriesRequiringCredential(list); len(got) != 0 {
		t.Fatalf("expected no registries requiring a ghcr credential, got %v", got)
	}
}
