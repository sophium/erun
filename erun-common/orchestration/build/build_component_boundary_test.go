package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/buildtarget"
	"github.com/sophium/erun/erun-common/base/seam/projectroot"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
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

func boundaryResolvers(projectRoot, cwd string) (projectroot.ProjectFinderFunc, buildtarget.BuildContextResolverFunc) {
	findProjectRoot := func() (string, string, error) { return "team", projectRoot, nil }
	resolveBuildContext := func() (model.DockerBuildContext, error) { return dockerspec.DockerBuildContextAtDir(cwd) }
	return findProjectRoot, resolveBuildContext
}

func boundaryTarget(projectRoot string) model.DockerCommandTarget {
	return model.DockerCommandTarget{ProjectRoot: projectRoot, Environment: "dev", VersionOverride: "1.0.0", NoIncremental: true}
}

// The build refusals are integration goldens (build/dry_run_unconsumed_test_stage_is_refused,
// build/dry_run_from_an_e2e_suite_directory_is_refused); push resolves through its own entry.
func TestPushResolutionRefusesAnUnconsumedTestStage(t *testing.T) {
	projectRoot, componentDir, _ := boundaryProject(t, unconsumedTestStageDockerfileContent)
	findProjectRoot, resolveBuildContext := boundaryResolvers(projectRoot, componentDir)

	if _, err := ResolveDockerPushExecution(core.Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, boundaryTarget(projectRoot)); err == nil {
		t.Fatal("expected erun push to refuse a Dockerfile whose test stage is never consumed")
	}
}

func TestPushResolutionRefusesAnE2ESuiteDockerfile(t *testing.T) {
	projectRoot, _, suiteDir := boundaryProject(t, consumedTestStageDockerfileContent)
	findProjectRoot, resolveBuildContext := boundaryResolvers(projectRoot, suiteDir)

	if _, _, err := ResolveDockerPushSpec(core.Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, boundaryTarget(projectRoot)); err == nil {
		t.Fatal("expected erun push to refuse an e2e suite's Dockerfile")
	}
}
