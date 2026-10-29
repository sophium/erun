package run

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
