package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimedefaults"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

func TestPushToolRejectsRepoRootWithoutComponent(t *testing.T) {
	projectRoot := t.TempDir()
	moduleRoot := filepath.Join(projectRoot, "tenant-a-devops")
	componentDir := filepath.Join(moduleRoot, "docker", "tenant-a-devops")
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		t.Fatalf("mkdir component dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}

	handler := PushTool(runtimedefaults.NormalizeRuntimeConfig(runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{
			Environment: configpkg.DefaultEnvironment,
			RepoPath:    projectRoot,
		},
	}))

	// A version gets past the version gate to the rejection under test.
	_, _, err := handler(context.Background(), nil, PushInput{Version: "1.0.0"})
	if err == nil {
		t.Fatal("expected missing Dockerfile error")
	}
	if err.Error() != "dockerfile not found in current directory" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// push publishes a built version and never mints one (erun-mcp/AGENTS.md).
func TestPushToolRequiresVersion(t *testing.T) {
	handler := PushTool(runtimedefaults.NormalizeRuntimeConfig(runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{
			Environment: configpkg.DefaultEnvironment,
			RepoPath:    t.TempDir(),
		},
	}))

	if _, _, err := handler(context.Background(), nil, PushInput{}); !errors.Is(err, ErrMissingPushVersion) {
		t.Fatalf("expected errMissingPushVersion, got %v", err)
	}
}
