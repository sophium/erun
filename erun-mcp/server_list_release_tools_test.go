package erunmcp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-mcp/tools/build"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

func runGitTestCommand(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

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

	handler := build.PushTool(normalizeRuntimeConfig(runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{
			Environment: configpkg.DefaultEnvironment,
			RepoPath:    projectRoot,
		},
	}))

	// A version gets past the version gate to the rejection under test.
	_, _, err := handler(context.Background(), nil, build.PushInput{Version: "1.0.0"})
	if err == nil {
		t.Fatal("expected missing Dockerfile error")
	}
	if err.Error() != "dockerfile not found in current directory" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// push publishes a built version and never mints one (erun-mcp/AGENTS.md).
func TestPushToolRequiresVersion(t *testing.T) {
	handler := build.PushTool(normalizeRuntimeConfig(runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{
			Environment: configpkg.DefaultEnvironment,
			RepoPath:    t.TempDir(),
		},
	}))

	if _, _, err := handler(context.Background(), nil, build.PushInput{}); !errors.Is(err, build.ErrMissingPushVersion) {
		t.Fatalf("expected errMissingPushVersion, got %v", err)
	}
}
