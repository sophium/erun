package setup

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

func TestGHCRRegistriesRequiringCredentialFiltersToGHCRBuildOrDeployRoles(t *testing.T) {
	list := config.ContainerRegistries{
		{Registry: "ghcr.io/sophium", Roles: []model.RegistryRole{model.RegistryRoleBuild, model.RegistryRoleDeploy}},
		{Registry: "ghcr.io/other", Roles: []model.RegistryRole{model.RegistryRoleFrom}},
		{Registry: "020362606330.dkr.ecr.eu-west-2.amazonaws.com/acme", Roles: []model.RegistryRole{model.RegistryRoleDeploy}},
		{Cluster: &config.ClusterRegistry{Service: "erun-registry"}, Roles: []model.RegistryRole{model.RegistryRoleBuild, model.RegistryRoleDeploy}},
	}

	got := ghcrRegistriesRequiringCredential(list)
	if len(got) != 1 || got[0] != "ghcr.io/sophium" {
		t.Fatalf("got %v, want only ghcr.io/sophium (build+deploy, non-cluster, ghcr)", got)
	}
}

func TestGHCRRegistriesRequiringCredentialDedupsAndPreservesOrder(t *testing.T) {
	list := config.ContainerRegistries{
		{Registry: "ghcr.io/b", Roles: []model.RegistryRole{model.RegistryRoleDeploy}},
		{Registry: "ghcr.io/a", Roles: []model.RegistryRole{model.RegistryRoleBuild}},
		{Registry: "ghcr.io/b", Roles: []model.RegistryRole{model.RegistryRoleBuild}},
	}

	got := ghcrRegistriesRequiringCredential(list)
	want := []string{"ghcr.io/b", "ghcr.io/a"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestGHCRRegistriesRequiringCredentialEmptyForNoGHCRRegistries(t *testing.T) {
	list := config.ContainerRegistries{
		{Registry: "020362606330.dkr.ecr.eu-west-2.amazonaws.com/acme", Roles: []model.RegistryRole{model.RegistryRoleBuild, model.RegistryRoleDeploy}},
	}
	if got := ghcrRegistriesRequiringCredential(list); len(got) != 0 {
		t.Fatalf("expected no registries requiring a ghcr credential, got %v", got)
	}
}
