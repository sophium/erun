package build

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimedefaults"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

// newComponentBuildFixture includes a root build.sh so the build script is not disabled.
func newComponentBuildFixture(t *testing.T) string {
	t.Helper()
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write build.sh: %v", err)
	}
	componentDir := filepath.Join(projectRoot, "acme-devops", "docker", "web")
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		t.Fatalf("mkdir component dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	return projectRoot
}

// A component build must still resolve a spec with platforms; one without
// builds nothing yet reports success.
func TestBuildToolBuildsComponentWithMultiPlatformSpec(t *testing.T) {
	projectRoot := newComponentBuildFixture(t)

	var captured *seam.DockerBuildSpec
	handler := BuildTool(runtimedefaults.NormalizeRuntimeConfig(runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{
			Environment: "dev",
			RepoPath:    projectRoot,
		},
		BuildScriptRunner: func(dir, path string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
			t.Fatal("unexpected build script call: --component must resolve the docker context directly")
			return nil
		},
		BuildDockerImage: func(spec seam.DockerBuildSpec, stdout, stderr io.Writer) error {
			captured = &spec
			return nil
		},
	}))

	_, output, err := handler(context.Background(), nil, BuildInput{Component: "web", NoIncremental: true})
	if err != nil {
		t.Fatalf("buildTool failed: %v", err)
	}
	if !output.Executed {
		t.Fatalf("expected execution output, got %+v", output)
	}
	if captured == nil {
		t.Fatal("expected the docker image builder to be invoked")
	}
	wantPlatforms := []string{"linux/amd64", "linux/arm64"}
	if !slices.Equal(captured.Platforms, wantPlatforms) {
		t.Fatalf("resolved component build spec has the wrong platforms, so it builds nothing real: got %v want %v", captured.Platforms, wantPlatforms)
	}
	if captured.DockerfilePath == "" {
		t.Fatalf("expected a resolved Dockerfile path, got %+v", captured)
	}
}
