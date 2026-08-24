package eruncommon

import (
	"strings"
	"testing"
)

func TestGHCRRegistriesRequiringCredentialFiltersToGHCRBuildOrDeployRoles(t *testing.T) {
	list := ContainerRegistries{
		{Registry: "ghcr.io/sophium", Roles: []RegistryRole{RegistryRoleBuild, RegistryRoleDeploy}},
		{Registry: "ghcr.io/other", Roles: []RegistryRole{RegistryRoleFrom}},
		{Registry: "020362606330.dkr.ecr.eu-west-2.amazonaws.com/acme", Roles: []RegistryRole{RegistryRoleDeploy}},
		{Cluster: &ClusterRegistry{Service: "erun-registry"}, Roles: []RegistryRole{RegistryRoleBuild, RegistryRoleDeploy}},
	}

	got := ghcrRegistriesRequiringCredential(list)
	if len(got) != 1 || got[0] != "ghcr.io/sophium" {
		t.Fatalf("got %v, want only ghcr.io/sophium (build+deploy, non-cluster, ghcr)", got)
	}
}

func TestGHCRRegistriesRequiringCredentialDedupsAndPreservesOrder(t *testing.T) {
	list := ContainerRegistries{
		{Registry: "ghcr.io/b", Roles: []RegistryRole{RegistryRoleDeploy}},
		{Registry: "ghcr.io/a", Roles: []RegistryRole{RegistryRoleBuild}},
		{Registry: "ghcr.io/b", Roles: []RegistryRole{RegistryRoleBuild}},
	}

	got := ghcrRegistriesRequiringCredential(list)
	want := []string{"ghcr.io/b", "ghcr.io/a"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestGHCRRegistriesRequiringCredentialEmptyForNoGHCRRegistries(t *testing.T) {
	list := ContainerRegistries{
		{Registry: "020362606330.dkr.ecr.eu-west-2.amazonaws.com/acme", Roles: []RegistryRole{RegistryRoleBuild, RegistryRoleDeploy}},
	}
	if got := ghcrRegistriesRequiringCredential(list); len(got) != 0 {
		t.Fatalf("expected no registries requiring a ghcr credential, got %v", got)
	}
}

// The script mirrors resolveGHCRBasicAuth's three routes; assert each is
// checked and that the host (not the full registry/namespace) is what gets
// grepped, since docker config keys registries by host.
func TestRemoteGHCRCredentialCheckScriptChecksAllThreeRoutes(t *testing.T) {
	script := remoteGHCRCredentialCheckScript("ghcr.io/sophium")
	for _, want := range []string{
		`$HOME/.docker/config.json`,
		"gh auth token -h github.com",
		"GH_TOKEN",
		"GITHUB_TOKEN",
		"'ghcr.io'",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("expected script to contain %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "ghcr.io/sophium") {
		t.Fatalf("script must grep the registry host, not the namespaced path:\n%s", script)
	}
}
