package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
)

func writeDockerPlatformsProjectConfig(t *testing.T, contents string) string {
	t.Helper()
	projectRoot := t.TempDir()
	configDir := filepath.Join(projectRoot, config.ProjectConfigDir)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) failed: %v", configDir, err)
	}
	if err := os.WriteFile(filepath.Join(configDir, config.ConfigFile), []byte(contents), 0o644); err != nil {
		t.Fatalf("writing test project config failed: %v", err)
	}
	return projectRoot
}
