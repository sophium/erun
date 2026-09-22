package eruncommon

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writePushableProjectConfig writes a project whose only configured platform is
// the amd64 pin, plus one docker build context the push path can resolve. It
// mirrors the shape a real tenant has: the pin is project-wide, and the build
// context sits under the devops module's docker/ directory.
func writePushableProjectConfig(t *testing.T) (projectRoot string, buildContext DockerBuildContext) {
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

	return projectRoot, DockerBuildContext{Dir: contextDir, DockerfilePath: dockerfilePath}
}

func pushResolversForTest(projectRoot string, buildContext DockerBuildContext) (ProjectFinderFunc, BuildContextResolverFunc) {
	findProjectRoot := func() (string, string, error) { return projectRoot, "", nil }
	resolveBuildContext := func() (DockerBuildContext, error) { return buildContext, nil }
	return findProjectRoot, resolveBuildContext
}

// TestPublishPathResolvesEveryPlatformDespiteTheProjectPin is the reproduction
// of the amd64-only release: `erun release` publishes nothing, so a version's
// artifacts reach the registry through `erun push --version <v>`. The project
// pin's own comment promises "published images are unaffected" because a
// release build never consults it -- but the publish path did, so the released
// version got an amd64-only manifest and every aarch64 environment was left
// with no arm64 image to pull, stranded on the last version that still shipped
// one. The pin may narrow a local build; it must not narrow what is published.
func TestPublishPathResolvesEveryPlatformDespiteTheProjectPin(t *testing.T) {
	projectRoot, buildContext := writePushableProjectConfig(t)
	findProjectRoot, resolveBuildContext := pushResolversForTest(projectRoot, buildContext)

	target := DockerCommandTarget{
		Environment:     "code1",
		ProjectRoot:     projectRoot,
		VersionOverride: "1.0.291",
		NoIncremental:   true,
	}

	execution, err := ResolveDockerPushExecution(Context{}, nil, findProjectRoot, resolveBuildContext, nil, target)
	if err != nil {
		t.Fatalf("ResolveDockerPushExecution failed: %v", err)
	}
	builds := execution.builds
	if len(builds) == 0 {
		t.Fatal("ResolveDockerPushExecution resolved no builds to publish")
	}
	if got := builds[0].Platforms; !reflect.DeepEqual(got, multiPlatformDockerBuilds) {
		t.Errorf("ResolveDockerPushExecution published platforms %v, want %v: a push is what puts a released version in the registry, so the project's amd64-only pin must not reach the published manifest",
			got, multiPlatformDockerBuilds)
	}

	// The single-context path `erun push` takes when a Dockerfile is in the
	// current directory resolves the same policy, and must not diverge from it.
	pushSpec, resolvedBuild, err := ResolveDockerPushSpec(Context{}, nil, findProjectRoot, resolveBuildContext, nil, target)
	if err != nil {
		t.Fatalf("ResolveDockerPushSpec failed: %v", err)
	}
	if resolvedBuild == nil {
		t.Fatal("ResolveDockerPushSpec resolved no build")
	}
	if got := resolvedBuild.Platforms; !reflect.DeepEqual(got, multiPlatformDockerBuilds) {
		t.Errorf("ResolveDockerPushSpec published platforms %v, want %v", got, multiPlatformDockerBuilds)
	}
	if got := pushSpec.Image.Tag; got != "ghcr.io/sophium/widget:1.0.291" {
		t.Errorf("pushed tag = %q, want %q", got, "ghcr.io/sophium/widget:1.0.291")
	}
}
