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

// A build that plans no work at all must not report success: it builds no
// image and runs no gate, so its exit code would be indistinguishable from a
// real pass for the caller that reads it as a verdict.
func TestRunBuildExecutionRefusesAPlanThatDoesNothing(t *testing.T) {
	err := RunBuildExecution(core.Context{DryRun: true}, shared.BuildExecutionSpec{}, nil, nil, nil, nil, cloud.CloudDependencies{})
	if err == nil {
		t.Fatal("expected a build that plans no work to fail instead of reporting success")
	}
	if !errors.Is(err, build.ErrBuildPlanEmpty) {
		t.Fatalf("expected ErrBuildPlanEmpty, got %T: %v", err, err)
	}
}

// The contrast the failure direction depends on: a project build script is real
// work even though it mints no image version, so it stays a success. Only a
// plan with nothing at all is refused.
func TestRunBuildExecutionAcceptsAScriptOnlyPlan(t *testing.T) {
	execution := shared.BuildExecutionSpec{Script: &shared.ScriptSpec{Dir: t.TempDir(), Path: "./build.sh"}}
	if err := RunBuildExecution(core.Context{DryRun: true}, execution, nil, nil, nil, nil, cloud.CloudDependencies{}); err != nil {
		t.Fatalf("expected a script-only plan to run, got %v", err)
	}
}

// An empty plan is refused in dry-run too: the preview must show the failure
// rather than print an empty plan and exit zero.
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
