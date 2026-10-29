package config

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
)

func TestContainerRegistriesValidateRejectsBothRegistryAndCluster(t *testing.T) {
	list := ContainerRegistries{{
		Registry: "ghcr.io/sophium",
		Cluster:  &ClusterRegistry{},
		Roles:    []model.RegistryRole{model.RegistryRoleBuild, model.RegistryRoleDeploy},
	}}
	if err := list.Validate(); err == nil {
		t.Fatal("expected error when both registry and cluster are set")
	}
}

func TestContainerRegistriesConcreteRequiresResolver(t *testing.T) {
	list := ContainerRegistries{{Cluster: &ClusterRegistry{}, Roles: []model.RegistryRole{model.RegistryRoleBuild, model.RegistryRoleDeploy}}}
	if _, err := list.Concrete(nil); err == nil {
		t.Fatal("expected error when resolver is nil for a cluster entry")
	}
}
