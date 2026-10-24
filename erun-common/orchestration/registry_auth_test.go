package orchestration

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/infra/registry"
)

func b64Auth(userPass string) string {
	return base64.StdEncoding.EncodeToString([]byte(userPass))
}

func writeDockerConfig(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(contents), 0o600); err != nil {
		t.Fatalf("write docker config: %v", err)
	}
	return dir
}

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
