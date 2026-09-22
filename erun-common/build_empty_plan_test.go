package eruncommon

import (
	"errors"
	"testing"
)

// A build that plans no work at all must not report success: it builds no
// image and runs no gate, so its exit code would be indistinguishable from a
// real pass for the caller that reads it as a verdict.
func TestRunBuildExecutionRefusesAPlanThatDoesNothing(t *testing.T) {
	err := RunBuildExecution(Context{DryRun: true}, BuildExecutionSpec{}, nil, nil, nil, nil, CloudDependencies{})
	if err == nil {
		t.Fatal("expected a build that plans no work to fail instead of reporting success")
	}
	if !errors.Is(err, ErrBuildPlanEmpty) {
		t.Fatalf("expected ErrBuildPlanEmpty, got %T: %v", err, err)
	}
}

// The contrast the failure direction depends on: a project build script is real
// work even though it mints no image version, so it stays a success. Only a
// plan with nothing at all is refused.
func TestRunBuildExecutionAcceptsAScriptOnlyPlan(t *testing.T) {
	execution := BuildExecutionSpec{script: &scriptSpec{Dir: t.TempDir(), Path: "./build.sh"}}
	if err := RunBuildExecution(Context{DryRun: true}, execution, nil, nil, nil, nil, CloudDependencies{}); err != nil {
		t.Fatalf("expected a script-only plan to run, got %v", err)
	}
}

// An empty plan is refused in dry-run too: the preview must show the failure
// rather than print an empty plan and exit zero.
func TestBuildExecutionPlansWorkDistinguishesScriptsFromNothing(t *testing.T) {
	if buildExecutionPlansWork(BuildExecutionSpec{}) {
		t.Error("an execution with no release, script, image, or chart plans no work")
	}
	if !buildExecutionPlansWork(BuildExecutionSpec{script: &scriptSpec{Path: "./build.sh"}}) {
		t.Error("a project build script is work")
	}
	if !buildExecutionPlansWork(BuildExecutionSpec{linuxBuilds: []scriptSpec{{Path: "./build.sh"}}}) {
		t.Error("a linux package build is work")
	}
}
