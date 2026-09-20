package eruncommon

import (
	"errors"
	"strings"
	"testing"
)

// A gate build's exit code is the entire verdict `erun review record-build
// --gate` records. When every image in the resolved plan promotes from the
// fingerprint cache -- and the plan carries no script or linux package build --
// that exit code is a green checkmark over a run that compiled, tested and
// executed nothing. These tests hold the refusal that keeps a promotion no-op
// from being recorded as an independent gate, and the contrast cases that keep
// it from becoming a refusal of real work.

func gateRefusalDockerBuild(tag string, promote bool) DockerBuildSpec {
	return DockerBuildSpec{
		Image:   DockerImageReference{ImageName: tag, Tag: tag},
		Promote: promote,
	}
}

// allPromotedGatePlan is the state the report described: a gate build whose
// every image was satisfied by the fingerprint cache, so the test stage the
// gate exists to run never executes.
func allPromotedGatePlan() BuildExecutionSpec {
	return BuildExecutionSpec{
		gate: true,
		dockerBuilds: []DockerBuildSpec{
			gateRefusalDockerBuild("erun-devops", true),
			gateRefusalDockerBuild("erun-backend-api", true),
		},
	}
}

// TestGateBuildRefusesAnAllPromotedPlanThatWouldExecuteNothing is the defect
// reproduction. Driven through RunBuildExecution -- the entry point `erun
// build` uses, whose returned error becomes the process exit code -- an
// all-promoted gate plan used to return nil, certifying a gate that ran
// nothing. It must now fail with ErrGateBuildNotRun.
func TestGateBuildRefusesAnAllPromotedPlanThatWouldExecuteNothing(t *testing.T) {
	err := RunBuildExecution(Context{DryRun: true}, allPromotedGatePlan(), nil, nil, nil, nil, CloudDependencies{})
	if err == nil {
		t.Fatal("expected an all-promoted gate build to fail instead of reporting a successful gate that executed nothing")
	}
	if !errors.Is(err, ErrGateBuildNotRun) {
		t.Fatalf("expected ErrGateBuildNotRun, got %T: %v", err, err)
	}
	for _, tag := range []string{"erun-devops", "erun-backend-api"} {
		if !strings.Contains(err.Error(), tag) {
			t.Errorf("expected the refusal to name the promoted image %q, got %v", tag, err)
		}
	}
	if !strings.Contains(err.Error(), "--no-incremental") {
		t.Errorf("expected the refusal to name the next action for a real gate, got %v", err)
	}
}

// The other half of the reproduction's contrast: the refusal is what --gate
// asks for, and an ordinary build over the very same promoted plan keeps
// today's behaviour. Without this, the fix would refuse every incremental
// build rather than only a gate that cannot be trusted.
func TestOrdinaryBuildStillReportsSuccessForAnAllPromotedPlan(t *testing.T) {
	execution := allPromotedGatePlan()
	execution.gate = false
	if err := RunBuildExecution(Context{DryRun: true}, execution, nil, nil, nil, nil, CloudDependencies{}); err != nil {
		t.Fatalf("expected an ordinary promoted plan to keep reporting success, got %v", err)
	}
}

// A gate build that really runs something is exactly what --gate is for: one
// image missing its fingerprint platform is a real build, and it must not be
// refused.
func TestGateBuildAcceptsAPlanWithAnImageThatReallyBuilds(t *testing.T) {
	execution := allPromotedGatePlan()
	execution.dockerBuilds[0].Promote = false
	if err := RunBuildExecution(Context{DryRun: true}, execution, nil, nil, nil, nil, CloudDependencies{}); err != nil {
		t.Fatalf("expected a gate build with one real image build to proceed, got %v", err)
	}
}

// A script or a linux package build runs unconditionally, so either one makes
// the gate a real execution however the images resolve. Refusing here would
// reject a gate that genuinely ran.
func TestGateBuildAcceptsAScriptOrLinuxPlan(t *testing.T) {
	scriptPlan := allPromotedGatePlan()
	scriptPlan.script = &scriptSpec{Dir: t.TempDir(), Path: "./build.sh"}
	if err := RunBuildExecution(Context{DryRun: true}, scriptPlan, nil, nil, nil, nil, CloudDependencies{}); err != nil {
		t.Fatalf("expected a gate build with a project script to proceed, got %v", err)
	}

	linuxPlan := allPromotedGatePlan()
	linuxPlan.script = nil
	linuxPlan.linuxBuilds = []scriptSpec{{Path: "./build.sh"}}
	if err := RunBuildExecution(Context{DryRun: true}, linuxPlan, nil, nil, nil, nil, CloudDependencies{}); err != nil {
		t.Fatalf("expected a gate build with a linux package build to proceed, got %v", err)
	}
}
