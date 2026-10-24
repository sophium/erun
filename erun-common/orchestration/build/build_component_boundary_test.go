package build

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

const unconsumedTestStageDockerfileContent = "" +
	"FROM golang:1.26.0 AS test\n" +
	"RUN go test ./... && go build -o /out/app ./...\n" +
	"\n" +
	"FROM scratch AS runtime\n" +
	"COPY app /app\n"

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

func boundaryResolvers(projectRoot, cwd string) (shared.ProjectFinderFunc, shared.BuildContextResolverFunc) {
	findProjectRoot := func() (string, string, error) { return "team", projectRoot, nil }
	resolveBuildContext := func() (model.DockerBuildContext, error) { return shared.DockerBuildContextAtDir(cwd) }
	return findProjectRoot, resolveBuildContext
}

func boundaryTarget(projectRoot string) model.DockerCommandTarget {
	return model.DockerCommandTarget{ProjectRoot: projectRoot, Environment: "dev", VersionOverride: "1.0.0", NoIncremental: true}
}

// A `test` stage nothing consumes is pruned by BuildKit, so its tests never run.
func TestBuildResolutionRefusesAnUnconsumedTestStage(t *testing.T) {
	projectRoot, componentDir, _ := boundaryProject(t, unconsumedTestStageDockerfileContent)
	findProjectRoot, resolveBuildContext := boundaryResolvers(projectRoot, componentDir)

	_, err := ResolveBuildExecution(core.Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, boundaryTarget(projectRoot))
	if err == nil {
		t.Fatal("expected erun build to refuse a Dockerfile whose test stage is never consumed")
	}
	for _, want := range []string{filepath.Join(componentDir, "Dockerfile"), `stage named "test" that no later stage consumes`, "COPY --from=test <artifact> <dest>", "FROM test"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}

	if _, err := ResolveDockerPushExecution(core.Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, boundaryTarget(projectRoot)); err == nil {
		t.Fatal("expected erun push to refuse the same Dockerfile")
	}
}

func TestBuildResolutionRefusesAnE2ESuiteDockerfile(t *testing.T) {
	projectRoot, _, suiteDir := boundaryProject(t, consumedTestStageDockerfileContent)
	findProjectRoot, resolveBuildContext := boundaryResolvers(projectRoot, suiteDir)

	_, err := ResolveBuildExecution(core.Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, boundaryTarget(projectRoot))
	if err == nil {
		t.Fatal("expected erun build to refuse an e2e suite's Dockerfile")
	}
	if errors.Is(err, shared.ErrDockerBuildContextNotFound) {
		t.Fatalf("an e2e suite must be refused with a pointer, not read as no build context: %v", err)
	}
	if !strings.Contains(err.Error(), "erun e2e --component console") {
		t.Fatalf("refusal %q does not point at erun e2e for the suite", err)
	}

	if _, _, err := ResolveDockerPushSpec(core.Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, boundaryTarget(projectRoot)); err == nil {
		t.Fatal("expected erun push to refuse an e2e suite's Dockerfile")
	}
}
