package main

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func flagValue(args []string, flag string) []string {
	var out []string
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			out = append(out, args[i+1])
		}
	}
	return out
}

func hasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

func TestInitFlagsCarryPullSecretsAndRuntimeRegistry(t *testing.T) {
	args := appendInitOptionalFlags(nil, uimodel.Selection{
		RuntimeImage:     "registry.example.com/acme/devops",
		RuntimeRegistry:  "ghcr.io/sophium",
		ImagePullSecrets: []string{"ecr-pull", " ", "ghcr-pull"},
	})

	if got := flagValue(args, "--runtime-registry"); len(got) != 1 || got[0] != "ghcr.io/sophium" {
		t.Fatalf("--runtime-registry: got %v", got)
	}
	secrets := flagValue(args, "--image-pull-secret")
	if len(secrets) != 2 || secrets[0] != "ecr-pull" || secrets[1] != "ghcr-pull" {
		t.Fatalf("blank secrets must be dropped and the rest repeated: got %v", secrets)
	}
}

func TestInitFlagsKeepTheThreeRegistryChoicesExclusive(t *testing.T) {
	hosted := appendInitOptionalFlags(nil, uimodel.Selection{ErunRegistry: true, ContainerRegistry: "ignored.example.com"})
	if !hasFlag(hosted, "--erun-registry") {
		t.Fatal("hosted registry selection must emit --erun-registry")
	}
	if hasFlag(hosted, "--container-registry") {
		t.Fatalf("hosted must suppress --container-registry: %s", strings.Join(hosted, " "))
	}
	if hasFlag(hosted, "--cluster-registry") {
		t.Fatalf("hosted must not also select the cluster registry: %s", strings.Join(hosted, " "))
	}

	cluster := appendInitOptionalFlags(nil, uimodel.Selection{ClusterRegistry: true, ContainerRegistry: "ignored.example.com"})
	if !hasFlag(cluster, "--cluster-registry") || hasFlag(cluster, "--container-registry") || hasFlag(cluster, "--erun-registry") {
		t.Fatalf("cluster selection is exclusive too: %s", strings.Join(cluster, " "))
	}

	static := appendInitOptionalFlags(nil, uimodel.Selection{ContainerRegistry: "registry.example.com"})
	if got := flagValue(static, "--container-registry"); len(got) != 1 || got[0] != "registry.example.com" {
		t.Fatalf("a plain registry string still passes through: %v", got)
	}
}

func TestRuntimeRegistrySurvivesAnExclusiveRegistryChoice(t *testing.T) {
	for _, selection := range []uimodel.Selection{
		{ErunRegistry: true, RuntimeRegistry: "ghcr.io/sophium"},
		{ClusterRegistry: true, RuntimeRegistry: "ghcr.io/sophium"},
	} {
		args := appendInitOptionalFlags(nil, selection)
		if got := flagValue(args, "--runtime-registry"); len(got) != 1 {
			t.Fatalf("runtime registry dropped: %s", strings.Join(args, " "))
		}
	}
}
