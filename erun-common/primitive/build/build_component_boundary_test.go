package build

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func boundaryTarget(projectRoot string) model.DockerCommandTarget {
	return model.DockerCommandTarget{ProjectRoot: projectRoot, Environment: "dev", VersionOverride: "1.0.0", NoIncremental: true}
}

func TestDockerfileTestStageConsumerForms(t *testing.T) {
	cases := []struct {
		name     string
		consumer string
		consumed bool
	}{
		{"copy with other flags first", "COPY --link --chown=0:0 --from=test /out/app /app", true},
		{"run bind mount", "RUN --mount=type=bind,from=test,source=/out,target=/t true", true},
		{"stage built from test", "FROM test AS dist", true},
		{"pinned stage built from test", "FROM --platform=$BUILDPLATFORM test AS dist", true},
		{"commented consumer", "# COPY --from=test /out/app /app", false},
		{"prefixed stage name", "COPY --from=test-toolchain /usr/bin/curl /usr/bin/curl", false},
		{"prefixed mount source", "RUN --mount=type=bind,from=testing,target=/t true", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTestDockerfile(t, t.TempDir(), "FROM golang:1.26.0 AS test\nRUN go build -o /out/app ./...\n\nFROM scratch\n"+tc.consumer+"\n")
			if got := dockerspec.DockerfileHasConsumedTestStage(path); got != tc.consumed {
				t.Fatalf("dockerfileHasConsumedTestStage = %v, want %v", got, tc.consumed)
			}
			err := dockerspec.CheckDockerfileTestStageConsumed(path)
			if (err == nil) != tc.consumed {
				t.Fatalf("checkDockerfileTestStageConsumed = %v, want refusal %v", err, !tc.consumed)
			}
		})
	}
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

func TestCheckNotE2ESuiteDockerfileScopesToDevopsE2E(t *testing.T) {
	for path, refused := range map[string]bool{
		"/repo/team-devops/docker/console/Dockerfile": false,
		"/repo/tools/e2e/console/Dockerfile":          false,
		"/repo/team-devops/e2e/Dockerfile":            false,
		"":                                            false,
	} {
		if err := dockerspec.CheckNotE2ESuiteDockerfile(path); (err != nil) != refused {
			t.Errorf("checkNotE2ESuiteDockerfile(%q) = %v, want refusal %v", path, err, refused)
		}
	}
}

func testOutcomeBuild(t *testing.T, dockerfile string) dockerspec.DockerBuildSpec {
	t.Helper()
	dir := t.TempDir()
	return dockerspec.DockerBuildSpec{
		ContextDir:     dir,
		DockerfilePath: writeTestDockerfile(t, dir, dockerfile),
		Image:          model.DockerImageReference{ImageName: "api", Tag: "ghcr.io/sophium/api:1.0.0"},
		Platforms:      []string{"linux/amd64"},
	}
}

func runOutcomeBuild(t *testing.T, buildInput dockerspec.DockerBuildSpec, build DockerImageBuilderFunc) (string, error) {
	t.Helper()
	var log bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log)}
	err := executeDockerBuild(ctx, buildInput, build, io.Discard, io.Discard)
	return log.String(), err
}

// A fingerprint promotion reuses an earlier result and must not read as a fresh pass.
func TestBuildReportsEachComponentsTestOutcome(t *testing.T) {
	succeed := func(dockerspec.DockerBuildSpec, io.Writer, io.Writer) error { return nil }

	rebuilt := testOutcomeBuild(t, consumedTestStageDockerfileContent)
	rebuilt.Fingerprint = "abc123"
	promoted := rebuilt
	promoted.Promote = true

	cases := []struct {
		name  string
		build dockerspec.DockerBuildSpec
		run   DockerImageBuilderFunc
		want  string
	}{
		{"built without incremental", testOutcomeBuild(t, consumedTestStageDockerfileContent), succeed, "api: test: passed\n"},
		{"promote fell back to a rebuild", promoted, func(input dockerspec.DockerBuildSpec, _, _ io.Writer) error {
			input.Cache.NotePromoteFallback()
			return nil
		}, "api: test: passed\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log, err := runOutcomeBuild(t, tc.build, tc.run)
			if err != nil {
				t.Fatalf("executeDockerBuild: %v", err)
			}
			if log != tc.want {
				t.Fatalf("outcome line = %q, want %q", log, tc.want)
			}
		})
	}
}

func TestFailedOrDryRunBuildReportsNoTestOutcome(t *testing.T) {
	build := testOutcomeBuild(t, consumedTestStageDockerfileContent)
	log, err := runOutcomeBuild(t, build, func(dockerspec.DockerBuildSpec, io.Writer, io.Writer) error { return errors.New("test stage failed") })
	if err == nil || strings.Contains(log, "test:") {
		t.Fatalf("a failed build must report no test outcome, got err %v, log %q", err, log)
	}

	var dry bytes.Buffer
	ctx := core.Context{DryRun: true, Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &dry, &dry)}
	if err := RunDockerBuild(ctx, build, func(dockerspec.DockerBuildSpec, io.Writer, io.Writer) error {
		t.Fatal("a dry run must not build")
		return nil
	}); err != nil {
		t.Fatalf("RunDockerBuild dry-run: %v", err)
	}
	if strings.Contains(dry.String(), "test:") {
		t.Fatalf("a dry run decides nothing about tests and must not report an outcome, got %q", dry.String())
	}
}

func TestConcurrentBuildsReportTestOutcomesInBuildOrder(t *testing.T) {
	dir := t.TempDir()
	slow := buildSpecFromDockerfile(t, dir, "slow", "debian:bookworm")
	slow.Image.ImageName = "slow"
	fast := buildSpecFromDockerfile(t, dir, "fast", "debian:bookworm")
	fast.Image.ImageName = "fast"
	ordered, dependencies := resolveTestDependencies(t, []dockerspec.DockerBuildSpec{slow, fast})

	var released sync.WaitGroup
	released.Add(1)
	build := func(spec dockerspec.DockerBuildSpec, _, _ io.Writer) error {
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
