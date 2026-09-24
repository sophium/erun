package eruncommon

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
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

// The tests below are the second half of the same false green, and the half the
// plan-level refusal above cannot reach. There, erun's fingerprint cache
// satisfied every image and no docker build ran at all. Here every image really
// is built, docker is genuinely invoked, and BuildKit -- a cache *below* the one
// erun can see -- serves the Dockerfile's test stage from its own layer cache.
// The plan names real work, the exit code is 0, and `make check` never executed.
//
// These drive the umbrella that brackets a build and decides what the finished
// run reports, handing the stub builder the verbatim BuildKit streams captured
// from real builds (see build_gate_test_stage_evidence_test.go), so the
// reproduction runs the same code a real `erun build` runs.
//
// There is deliberately no `--gate` parameter here any more. Which answer is
// correct used to depend on it, and that was the defect: the run that reads a
// build's exit code as a verdict is the plain one.

// runGateBuildThroughTheUmbrella builds one gate image with the stub builder
// reporting the given BuildKit stream, and returns what the run's outcome would
// be.
func runGateBuildThroughTheUmbrella(t *testing.T, build DockerBuildSpec, buildOutput string) error {
	t.Helper()
	var log bytes.Buffer
	ctx, finish := traceBuildUmbrella(Context{Logger: NewLoggerWithWriters(VerbosityInfo, &log, &log)}, []DockerBuildSpec{build})

	err := RunDockerBuild(ctx, build, func(input DockerBuildSpec, stdout, stderr io.Writer) error {
		if input.PlatformObserver == nil {
			t.Fatal("expected the build to carry a platform observer, as every real build does")
		}
		input.PlatformObserver("linux/amd64", 2*time.Second, nil, nil, buildOutput)
		return nil
	})
	finish(&err)
	return err
}

// TestGateBuildRefusesATestStageBuildKitReplayed holds the refusal for the run
// that does declare itself the gate. Driven through the umbrella with the
// captured stream of a real warm build -- a gate image whose test stage BuildKit
// served entirely from its layer cache -- such a run must fail with
// ErrGateTestStageReplayed rather than certify a tree whose `make check` never
// executed.
func TestGateBuildRefusesATestStageBuildKitReplayed(t *testing.T) {
	err := runGateBuildThroughTheUmbrella(t, gateBuildFixture("erun-devops"), cachedTestStageBuildOutput)
	if err == nil {
		t.Fatal("expected a build whose test stage BuildKit replayed to fail instead of certifying a gate that never ran make check")
	}
	if !errors.Is(err, ErrGateTestStageReplayed) {
		t.Fatalf("expected ErrGateTestStageReplayed, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "erun-devops") {
		t.Errorf("expected the refusal to name the replayed image, got %v", err)
	}
	if !strings.Contains(err.Error(), "docker builder prune") {
		t.Errorf("expected the refusal to name the next action that actually clears this cache, got %v", err)
	}
	// The sibling refusal's remedy is the wrong one here and must not be
	// prescribed: --no-incremental bypasses erun's fingerprint cache, which is a
	// different cache, so a replayed stage replays again under it. Naming the fly
	// in the ointment is the point -- what must not appear is the sibling's
	// instruction to re-run with it.
	if strings.Contains(err.Error(), "re-run with --no-incremental") {
		t.Errorf("expected the layer-cache refusal to not prescribe --no-incremental, which cannot clear BuildKit's cache, got %v", err)
	}
}

// A gate that really ran its test stage is exactly what --gate is for. The cold
// build's own stream -- every instruction of the test stage DONE -- must not be
// refused, or the guard would reject the runs it exists to bless.
func TestGateBuildAcceptsATestStageThatReallyRan(t *testing.T) {
	if err := runGateBuildThroughTheUmbrella(t, gateBuildFixture("erun-devops"), liveTestStageBuildOutput); err != nil {
		t.Fatalf("expected a gate build that executed its test stage to proceed, got %v", err)
	}
}

// A one-source-file change: unchanged early steps come back CACHED while the
// COPY carrying the change and the gate after it both run. The stage did execute,
// so refusing it would be a false alarm on the ordinary case.
func TestGateBuildAcceptsAPartiallyCachedTestStage(t *testing.T) {
	if err := runGateBuildThroughTheUmbrella(t, gateBuildFixture("erun-devops"), partialTestStageBuildOutput); err != nil {
		t.Fatalf("expected a build whose test stage ran despite cached early steps to proceed, got %v", err)
	}
}

// TestPlainBuildRefusesATestStageBuildKitReplayed is the defect reproduction for
// the run the guard used to leave unguarded. `--gate` was what armed the refusal,
// so the very stream above -- a warm build BuildKit served the whole test stage
// from its layer cache -- was printed as REPLAYED and then *accepted* by the plain
// `erun build` that both documented gate flows actually run: `erun exec gate-merge`
// -> `erun build` -> `erun review record-build --gate` reads its exit code as the
// queue's verdict, and the erun-merge skill's READY rung turns it into READY. A
// build that ran no `make check` must not exit zero whatever flags it was given,
// so this case now fails with ErrGateTestStageReplayed. It fails on the pre-fix
// code for the reason the report gave -- the guard was armed by the flag, and this
// build passes none.
func TestPlainBuildRefusesATestStageBuildKitReplayed(t *testing.T) {
	err := runGateBuildThroughTheUmbrella(t, gateBuildFixture("erun-devops"), cachedTestStageBuildOutput)
	if err == nil {
		t.Fatal("expected an ordinary build whose test stage BuildKit replayed to fail instead of exiting 0 over a suite that never ran")
	}
	if !errors.Is(err, ErrGateTestStageReplayed) {
		t.Fatalf("expected ErrGateTestStageReplayed, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "erun-devops") {
		t.Errorf("expected the refusal to name the replayed image, got %v", err)
	}
	// The remedy has to be reachable from the failure: pruning a shared cache is
	// the expensive answer, and --no-incremental is the wrong one (a different
	// cache), which the sibling test below holds the line on.
	if !strings.Contains(err.Error(), "--gate") {
		t.Errorf("expected the refusal to name the cheap remedy that re-runs just this stage, got %v", err)
	}
}

// Output that never mentioned the test stage is an absence of evidence, not a
// replay. A build that failed before reaching the stage, or a stream this parser
// cannot read, must not be refused on a guess -- the same best-effort contract
// the rest of the stream's parsers hold to.
func TestGateBuildAcceptsAStageTheBuilderSaidNothingAbout(t *testing.T) {
	if err := runGateBuildThroughTheUmbrella(t, gateBuildFixture("erun-devops"), ""); err != nil {
		t.Fatalf("expected a gate build with no evidence about its test stage to proceed rather than be refused on a guess, got %v", err)
	}
}
