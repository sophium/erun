package build

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

func boundaryProject(t *testing.T, componentDockerfile string) (projectRoot, componentDir, suiteDir string) {
	t.Helper()
	projectRoot = t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	componentDir = filepath.Join(projectRoot, "team-devops", "docker", "api")
	writeTestDockerfile(t, componentDir, componentDockerfile)
	suiteDir = filepath.Join(projectRoot, "team-devops", "e2e", "console")
	writeTestDockerfile(t, suiteDir, "FROM node:22.11.0 AS deps\n\nFROM deps AS e2e\nRUN npx playwright test\n")
	return projectRoot, componentDir, suiteDir
}

func boundaryResolvers(projectRoot, cwd string) (seam.ProjectFinderFunc, seam.BuildContextResolverFunc) {
	findProjectRoot := func() (string, string, error) { return "team", projectRoot, nil }
	resolveBuildContext := func() (model.DockerBuildContext, error) { return dockerspec.DockerBuildContextAtDir(cwd) }
	return findProjectRoot, resolveBuildContext
}

func TestComponentDiscoveryNeverReachesE2ESuites(t *testing.T) {
	projectRoot, componentDir, _ := boundaryProject(t, consumedTestStageDockerfileContent)
	findProjectRoot, resolveBuildContext := boundaryResolvers(projectRoot, projectRoot)

	contexts, err := ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, model.DockerCommandTarget{ProjectRoot: projectRoot})
	if err != nil {
		t.Fatalf("ResolveCurrentDockerBuildContexts: %v", err)
	}
	if len(contexts) != 1 || contexts[0].Dir != componentDir {
		t.Fatalf("expected only the docker/api component, got %+v", contexts)
	}

	if _, ok, err := FindComponentDockerBuildContext(projectRoot, "console"); err != nil || ok {
		t.Fatalf("FindComponentDockerBuildContext(console) = found %v, err %v; an e2e suite is not a component", ok, err)
	}
}

func testOutcomeBuild(t *testing.T, dockerfile string) seam.DockerBuildSpec {
	t.Helper()
	dir := t.TempDir()
	return seam.DockerBuildSpec{
		ContextDir:     dir,
		DockerfilePath: writeTestDockerfile(t, dir, dockerfile),
		Image:          model.DockerImageReference{ImageName: "api", Tag: "ghcr.io/sophium/api:1.0.0"},
		Platforms:      []string{"linux/amd64"},
	}
}

func runOutcomeBuild(t *testing.T, buildInput seam.DockerBuildSpec, build seam.DockerImageBuilderFunc) (string, error) {
	t.Helper()
	var log bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log)}
	err := executeDockerBuild(ctx, buildInput, build, io.Discard, io.Discard)
	return log.String(), err
}

// A promote that fell back to a rebuild ran the tests, so it must read as a fresh pass.
func TestBuildReportsEachComponentsTestOutcome(t *testing.T) {
	promoted := testOutcomeBuild(t, consumedTestStageDockerfileContent)
	promoted.Fingerprint = "abc123"
	promoted.Promote = true

	log, err := runOutcomeBuild(t, promoted, func(input seam.DockerBuildSpec, _, _ io.Writer) error {
		input.Cache.NotePromoteFallback()
		return nil
	})
	if err != nil {
		t.Fatalf("executeDockerBuild: %v", err)
	}
	if want := "api: test: passed\n"; log != want {
		t.Fatalf("outcome line = %q, want %q", log, want)
	}
}

func TestConcurrentBuildsReportTestOutcomesInBuildOrder(t *testing.T) {
	dir := t.TempDir()
	slow := buildSpecFromDockerfile(t, dir, "slow", "debian:bookworm")
	slow.Image.ImageName = "slow"
	fast := buildSpecFromDockerfile(t, dir, "fast", "debian:bookworm")
	fast.Image.ImageName = "fast"
	ordered, dependencies := resolveTestDependencies(t, []seam.DockerBuildSpec{slow, fast})

	var released sync.WaitGroup
	released.Add(1)
	build := func(spec seam.DockerBuildSpec, _, _ io.Writer) error {
		if spec.Image.ImageName == "fast" {
			released.Done()
			return nil
		}
		released.Wait()
		return nil
	}

	var log bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log), Stdout: io.Discard, Stderr: io.Discard}
	if err := runBuildDependencies(ctx, ordered, dependencies, build, 2); err != nil {
		t.Fatalf("run builds: %v", err)
	}
	if got, want := log.String(), "slow: test: none declared\nfast: test: none declared\n"; got != want {
		t.Fatalf("outcome lines = %q, want %q", got, want)
	}
}
