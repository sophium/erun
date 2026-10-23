package eruncommon

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const unconsumedTestStageDockerfileContent = "" +
	"FROM golang:1.26.0 AS test\n" +
	"RUN go test ./... && go build -o /out/app ./...\n" +
	"\n" +
	"FROM scratch AS runtime\n" +
	"COPY app /app\n"

// boundaryProject lays out a <tenant>-devops tree with one component under
// docker/ and one suite under e2e/, beside a VERSION the build can resolve.
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

func boundaryResolvers(projectRoot, cwd string) (ProjectFinderFunc, BuildContextResolverFunc) {
	findProjectRoot := func() (string, string, error) { return "team", projectRoot, nil }
	resolveBuildContext := func() (DockerBuildContext, error) { return DockerBuildContextAtDir(cwd) }
	return findProjectRoot, resolveBuildContext
}

func boundaryTarget(projectRoot string) DockerCommandTarget {
	return DockerCommandTarget{ProjectRoot: projectRoot, Environment: "dev", VersionOverride: "1.0.0", NoIncremental: true}
}

func TestDockerfileTestStageConsumerForms(t *testing.T) {
	cases := []struct {
		name     string
		consumer string
		consumed bool
	}{
		{"copy artifact", "COPY --from=test /out/app /app", true},
		{"copy with other flags first", "COPY --link --chown=0:0 --from=test /out/app /app", true},
		{"run bind mount", "RUN --mount=type=bind,from=test,source=/out,target=/t true", true},
		{"stage built from test", "FROM test AS dist", true},
		{"pinned stage built from test", "FROM --platform=$BUILDPLATFORM test AS dist", true},
		{"no consumer", "COPY app /app", false},
		{"commented consumer", "# COPY --from=test /out/app /app", false},
		{"prefixed stage name", "COPY --from=test-toolchain /usr/bin/curl /usr/bin/curl", false},
		{"prefixed mount source", "RUN --mount=type=bind,from=testing,target=/t true", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTestDockerfile(t, t.TempDir(), "FROM golang:1.26.0 AS test\nRUN go build -o /out/app ./...\n\nFROM scratch\n"+tc.consumer+"\n")
			if got := dockerfileHasConsumedTestStage(path); got != tc.consumed {
				t.Fatalf("dockerfileHasConsumedTestStage = %v, want %v", got, tc.consumed)
			}
			err := checkDockerfileTestStageConsumed(path)
			if (err == nil) != tc.consumed {
				t.Fatalf("checkDockerfileTestStageConsumed = %v, want refusal %v", err, !tc.consumed)
			}
		})
	}
}

// The reproduction: a Dockerfile declaring a `test` stage nothing consumes
// built, wrote its fingerprint and reported success, because BuildKit prunes
// the unreferenced stage and the tests never ran. Resolution must refuse it.
func TestBuildResolutionRefusesAnUnconsumedTestStage(t *testing.T) {
	projectRoot, componentDir, _ := boundaryProject(t, unconsumedTestStageDockerfileContent)
	findProjectRoot, resolveBuildContext := boundaryResolvers(projectRoot, componentDir)

	_, err := ResolveBuildExecution(Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, boundaryTarget(projectRoot))
	if err == nil {
		t.Fatal("expected erun build to refuse a Dockerfile whose test stage is never consumed")
	}
	for _, want := range []string{filepath.Join(componentDir, "Dockerfile"), `stage named "test" that no later stage consumes`, "COPY --from=test <artifact> <dest>", "FROM test"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}

	// erun push resolves its own builds; it must not publish what build refuses.
	if _, err := ResolveDockerPushExecution(Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, boundaryTarget(projectRoot)); err == nil {
		t.Fatal("expected erun push to refuse the same Dockerfile")
	}
}

func TestBuildResolutionAcceptsAConsumedTestStage(t *testing.T) {
	projectRoot, componentDir, _ := boundaryProject(t, consumedTestStageDockerfileContent)
	findProjectRoot, resolveBuildContext := boundaryResolvers(projectRoot, componentDir)

	execution, err := ResolveBuildExecution(Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, boundaryTarget(projectRoot))
	if err != nil {
		t.Fatalf("expected a consumed test stage to resolve, got %v", err)
	}
	if len(execution.dockerBuilds) != 1 || execution.dockerBuilds[0].Image.ImageName != "api" {
		t.Fatalf("expected the one api build, got %+v", execution.dockerBuilds)
	}
}

// The reproduction: run from an e2e suite's directory, erun build took its
// Dockerfile as a component and would fingerprint, promote and push it.
func TestBuildResolutionRefusesAnE2ESuiteDockerfile(t *testing.T) {
	projectRoot, _, suiteDir := boundaryProject(t, consumedTestStageDockerfileContent)
	findProjectRoot, resolveBuildContext := boundaryResolvers(projectRoot, suiteDir)

	_, err := ResolveBuildExecution(Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, boundaryTarget(projectRoot))
	if err == nil {
		t.Fatal("expected erun build to refuse an e2e suite's Dockerfile")
	}
	if errors.Is(err, ErrDockerBuildContextNotFound) {
		t.Fatalf("an e2e suite must be refused with a pointer, not read as no build context: %v", err)
	}
	if !strings.Contains(err.Error(), "erun e2e --component console") {
		t.Fatalf("refusal %q does not point at erun e2e for the suite", err)
	}

	if _, _, err := ResolveDockerPushSpec(Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, boundaryTarget(projectRoot)); err == nil {
		t.Fatal("expected erun push to refuse an e2e suite's Dockerfile")
	}
}

// Project-wide discovery walks docker/ only, so a suite beside it is never a
// component, by directory scan or by component name.
func TestComponentDiscoveryNeverReachesE2ESuites(t *testing.T) {
	projectRoot, componentDir, _ := boundaryProject(t, consumedTestStageDockerfileContent)
	findProjectRoot, resolveBuildContext := boundaryResolvers(projectRoot, projectRoot)

	contexts, err := ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, DockerCommandTarget{ProjectRoot: projectRoot})
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
		"/repo/team-devops/e2e/console/Dockerfile":    true,
		"/repo/team-devops/docker/console/Dockerfile": false,
		"/repo/tools/e2e/console/Dockerfile":          false,
		"/repo/team-devops/e2e/Dockerfile":            false,
		"":                                            false,
	} {
		if err := checkNotE2ESuiteDockerfile(path); (err != nil) != refused {
			t.Errorf("checkNotE2ESuiteDockerfile(%q) = %v, want refusal %v", path, err, refused)
		}
	}
}

func testOutcomeBuild(t *testing.T, dockerfile string) DockerBuildSpec {
	t.Helper()
	dir := t.TempDir()
	return DockerBuildSpec{
		ContextDir:     dir,
		DockerfilePath: writeTestDockerfile(t, dir, dockerfile),
		Image:          DockerImageReference{ImageName: "api", Tag: "ghcr.io/sophium/api:1.0.0"},
		Platforms:      []string{"linux/amd64"},
	}
}

func runOutcomeBuild(t *testing.T, buildInput DockerBuildSpec, build DockerImageBuilderFunc) (string, error) {
	t.Helper()
	var log bytes.Buffer
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, &log, &log)}
	err := executeDockerBuild(ctx, buildInput, build, io.Discard, io.Discard)
	return log.String(), err
}

// "Ran" and "reused" must stay distinct per component: a fingerprint promotion
// reuses an earlier tested result and must not read as a fresh pass.
func TestBuildReportsEachComponentsTestOutcome(t *testing.T) {
	succeed := func(DockerBuildSpec, io.Writer, io.Writer) error { return nil }

	rebuilt := testOutcomeBuild(t, consumedTestStageDockerfileContent)
	rebuilt.Fingerprint = "abc123"
	promoted := rebuilt
	promoted.Promote = true

	cases := []struct {
		name  string
		build DockerBuildSpec
		run   DockerImageBuilderFunc
		want  string
	}{
		{"no test stage", testOutcomeBuild(t, "FROM scratch\nCOPY app /app\n"), succeed, "api: test: none declared\n"},
		{"built without incremental", testOutcomeBuild(t, consumedTestStageDockerfileContent), succeed, "api: test: passed\n"},
		{"rebuilt on a fingerprint miss", rebuilt, succeed, "api: test: passed\n"},
		{"promoted", promoted, succeed, "api: test: reused (fingerprint fp-abc123)\n"},
		{"promote fell back to a rebuild", promoted, func(input DockerBuildSpec, _, _ io.Writer) error {
			input.cache.notePromoteFallback()
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
	log, err := runOutcomeBuild(t, build, func(DockerBuildSpec, io.Writer, io.Writer) error { return errors.New("test stage failed") })
	if err == nil || strings.Contains(log, "test:") {
		t.Fatalf("a failed build must report no test outcome, got err %v, log %q", err, log)
	}

	var dry bytes.Buffer
	ctx := Context{DryRun: true, Logger: NewLoggerWithWriters(VerbosityInfo, &dry, &dry)}
	if err := RunDockerBuild(ctx, build, func(DockerBuildSpec, io.Writer, io.Writer) error {
		t.Fatal("a dry run must not build")
		return nil
	}); err != nil {
		t.Fatalf("RunDockerBuild dry-run: %v", err)
	}
	if strings.Contains(dry.String(), "test:") {
		t.Fatalf("a dry run decides nothing about tests and must not report an outcome, got %q", dry.String())
	}
}

// Under concurrent dispatch the outcome lines follow the build order, not the
// order images finished in, so the stream is reproducible.
func TestConcurrentBuildsReportTestOutcomesInBuildOrder(t *testing.T) {
	dir := t.TempDir()
	slow := buildSpecFromDockerfile(t, dir, "slow", "debian:bookworm")
	slow.Image.ImageName = "slow"
	fast := buildSpecFromDockerfile(t, dir, "fast", "debian:bookworm")
	fast.Image.ImageName = "fast"
	ordered, dependencies := resolveTestDependencies(t, []DockerBuildSpec{slow, fast})

	var released sync.WaitGroup
	released.Add(1)
	build := func(spec DockerBuildSpec, _, _ io.Writer) error {
		if spec.Image.ImageName == "fast" {
			released.Done()
			return nil
		}
		released.Wait()
		return nil
	}

	var log bytes.Buffer
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, &log, &log), Stdout: io.Discard, Stderr: io.Discard}
	if err := runBuildDependencies(ctx, ordered, dependencies, build, 2); err != nil {
		t.Fatalf("run builds: %v", err)
	}
	if got, want := log.String(), "slow: test: none declared\nfast: test: none declared\n"; got != want {
		t.Fatalf("outcome lines = %q, want %q", got, want)
	}
}
