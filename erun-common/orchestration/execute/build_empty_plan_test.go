package execute

import (
	"errors"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	buildrun "github.com/sophium/erun/erun-common/orchestration/build"
	"github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// A build that plans no work must not exit zero: callers read the exit code as a verdict.
func TestRunBuildExecutionRefusesAPlanThatDoesNothing(t *testing.T) {
	err := RunBuildExecution(core.Context{DryRun: true}, shared.BuildExecutionSpec{}, nil, nil, nil, nil, cloud.CloudDependencies{})
	if err == nil {
		t.Fatal("expected a build that plans no work to fail instead of reporting success")
	}
	if !errors.Is(err, build.ErrBuildPlanEmpty) {
		t.Fatalf("expected ErrBuildPlanEmpty, got %T: %v", err, err)
	}
}

func TestBuildExecutionPlansWorkDistinguishesScriptsFromNothing(t *testing.T) {
	if buildrun.BuildExecutionPlansWork(shared.BuildExecutionSpec{}) {
		t.Error("an execution with no release, script, image, or chart plans no work")
	}
	if !buildrun.BuildExecutionPlansWork(shared.BuildExecutionSpec{Script: &shared.ScriptSpec{Path: "./build.sh"}}) {
		t.Error("a project build script is work")
	}
	if !buildrun.BuildExecutionPlansWork(shared.BuildExecutionSpec{LinuxPackages: []shared.LinuxPackageSpec{{Name: "erun-ui"}}}) {
		t.Error("a linux package is work")
	}
}
