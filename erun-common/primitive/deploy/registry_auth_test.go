package deploy

import (
	"testing"

	"github.com/sophium/erun/erun-common/infra/registry"
)

func useDockerConfigDir(t *testing.T, dir string) {
	t.Helper()
	prev := registry.DockerConfigDir
	registry.DockerConfigDir = func() string { return dir }
	t.Cleanup(func() { registry.DockerConfigDir = prev })
}

func useGHToken(t *testing.T, fn func(owner string) (string, bool)) {
	t.Helper()
	prev := registry.ResolveGHCRTokenViaGH
	registry.ResolveGHCRTokenViaGH = fn
	t.Cleanup(func() { registry.ResolveGHCRTokenViaGH = prev })
}
