package build

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func writePushableProjectConfig(t *testing.T) (projectRoot string, buildContext shared.DockerBuildContext) {
	t.Helper()

	projectRoot = writeDockerPlatformsProjectConfig(t, `docker:
    platforms:
        - linux/amd64
`)

	if err := os.WriteFile(filepath.Join(projectRoot, "VERSION"), []byte("1.0.291\n"), 0o644); err != nil {
		t.Fatalf("writing the test VERSION file failed: %v", err)
	}

	contextDir := filepath.Join(projectRoot, "erun-devops", "docker", "widget")
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) failed: %v", contextDir, err)
	}
	dockerfilePath := filepath.Join(contextDir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("writing the test Dockerfile failed: %v", err)
	}

	return projectRoot, shared.DockerBuildContext{Dir: contextDir, DockerfilePath: dockerfilePath}
}

func pushResolversForTest(projectRoot string, buildContext shared.DockerBuildContext) (shared.ProjectFinderFunc, shared.BuildContextResolverFunc) {
	findProjectRoot := func() (string, string, error) { return projectRoot, "", nil }
	resolveBuildContext := func() (shared.DockerBuildContext, error) { return buildContext, nil }
	return findProjectRoot, resolveBuildContext
}

// The project platform pin may narrow a local build, never what is published.
func TestPublishPathResolvesEveryPlatformDespiteTheProjectPin(t *testing.T) {
	projectRoot, buildContext := writePushableProjectConfig(t)
	findProjectRoot, resolveBuildContext := pushResolversForTest(projectRoot, buildContext)

	target := shared.DockerCommandTarget{
		Environment:     "code1",
		ProjectRoot:     projectRoot,
		VersionOverride: "1.0.291",
		NoIncremental:   true,
	}

	execution, err := ResolveDockerPushExecution(core.Context{}, nil, findProjectRoot, resolveBuildContext, nil, target)
	if err != nil {
		t.Fatalf("ResolveDockerPushExecution failed: %v", err)
	}
	builds := execution.Builds
	if len(builds) == 0 {
		t.Fatal("ResolveDockerPushExecution resolved no builds to publish")
	}
	if got := builds[0].Platforms; !reflect.DeepEqual(got, shared.MultiPlatformDockerBuilds) {
		t.Errorf("ResolveDockerPushExecution published platforms %v, want %v: a push is what puts a released version in the registry, so the project's amd64-only pin must not reach the published manifest",
			got, shared.MultiPlatformDockerBuilds)
	}

	pushSpec, resolvedBuild, err := ResolveDockerPushSpec(core.Context{}, nil, findProjectRoot, resolveBuildContext, nil, target)
	if err != nil {
		t.Fatalf("ResolveDockerPushSpec failed: %v", err)
	}
	if resolvedBuild == nil {
		t.Fatal("ResolveDockerPushSpec resolved no build")
	}
	if got := resolvedBuild.Platforms; !reflect.DeepEqual(got, shared.MultiPlatformDockerBuilds) {
		t.Errorf("ResolveDockerPushSpec published platforms %v, want %v", got, shared.MultiPlatformDockerBuilds)
	}
	if got := pushSpec.Image.Tag; got != "ghcr.io/sophium/widget:1.0.291" {
		t.Errorf("pushed tag = %q, want %q", got, "ghcr.io/sophium/widget:1.0.291")
	}
}
