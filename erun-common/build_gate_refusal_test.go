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
// The refusal below turns on the run's own declaration (`--gate`), and the pair
// of tests around the replay stream is what keeps that honest in both
// directions: a declared gate must fail on it, and a plain build -- whose rung
// claims only that the commit builds -- must not.

// runGateBuildThroughTheUmbrella builds one gate image with the stub builder
// reporting the given BuildKit stream, and returns the run's trace along with
// the outcome the umbrella hands back to its caller. gate is the run's own
// declaration that it is the merge queue's gate.
func runGateBuildThroughTheUmbrella(t *testing.T, build DockerBuildSpec, buildOutput string, gate bool) (string, error) {
	t.Helper()
	var log bytes.Buffer
	ctx, finish := traceBuildUmbrella(Context{Logger: NewLoggerWithWriters(VerbosityInfo, &log, &log)}, []DockerBuildSpec{build}, gate)

	err := RunDockerBuild(ctx, build, func(input DockerBuildSpec, stdout, stderr io.Writer) error {
		if input.PlatformObserver == nil {
			t.Fatal("expected the build to carry a platform observer, as every real build does")
		}
		input.PlatformObserver("linux/amd64", 2*time.Second, nil, nil, buildOutput)
		return nil
	})
	finish(&err)
	return log.String(), err
}

// TestGateBuildRefusesATestStageBuildKitReplayed holds the refusal for the run
// that does declare itself the gate. Driven through the umbrella with the
// captured stream of a real warm build -- a gate image whose test stage BuildKit
// served entirely from its layer cache -- such a run must fail with
// ErrGateTestStageReplayed rather than certify a tree whose `make check` never
// executed.
func TestGateBuildRefusesATestStageBuildKitReplayed(t *testing.T) {
	rendered, err := runGateBuildThroughTheUmbrella(t, gateBuildFixture("erun-devops"), cachedTestStageBuildOutput, true)
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
	// The refusal is the outcome, not a replacement for the report: the trace
	// still has to say what the builder did with the stage.
	if !strings.Contains(rendered, "REPLAYED") {
		t.Errorf("expected the refused run's trace to still report the replay, got:\n%s", rendered)
	}
}

// A gate that really ran its test stage is exactly what --gate is for. The cold
// build's own stream -- every instruction of the test stage DONE -- must not be
// refused, or the guard would reject the runs it exists to bless.
func TestGateBuildAcceptsATestStageThatReallyRan(t *testing.T) {
	if _, err := runGateBuildThroughTheUmbrella(t, gateBuildFixture("erun-devops"), liveTestStageBuildOutput, true); err != nil {
		t.Fatalf("expected a gate build that executed its test stage to proceed, got %v", err)
	}
}

// A one-source-file change: unchanged early steps come back CACHED while the
// COPY carrying the change and the gate after it both run. The stage did execute,
// so refusing it would be a false alarm on the ordinary case.
func TestGateBuildAcceptsAPartiallyCachedTestStage(t *testing.T) {
	if _, err := runGateBuildThroughTheUmbrella(t, gateBuildFixture("erun-devops"), partialTestStageBuildOutput, true); err != nil {
		t.Fatalf("expected a build whose test stage ran despite cached early steps to proceed, got %v", err)
	}
}

// TestPlainBuildAcceptsATestStageBuildKitReplayed is the defect reproduction. A
// plain `erun build` over this repo builds two gate-bearing images, and a diff
// that leaves either one's test-stage inputs untouched has that image's stage
// served wholly from BuildKit's layer cache. The refusal below is scoped to a
// declared gate, so the run used to fail there -- for a rung that claims only
// that the commit builds, which a replay on byte-identical inputs establishes
// exactly as well as the run that first executed the stage. Every branch whose
// diff did not touch both images was refused, and the erun-merge READY rung could
// not record a version from it.
//
// The stream is the one above, unchanged: the run must succeed, and the trace
// must still say REPLAYED rather than claim a gate that ran. On the pre-fix code
// this case fails with ErrGateTestStageReplayed for the reason the report gave --
// the guard was keyed on the replay alone, and this run declared no gate.
func TestPlainBuildAcceptsATestStageBuildKitReplayed(t *testing.T) {
	rendered, err := runGateBuildThroughTheUmbrella(t, gateBuildFixture("erun-devops"), cachedTestStageBuildOutput, false)
	if err != nil {
		t.Fatalf("expected a plain build whose test stage BuildKit replayed to build the commit, not to be refused for a gate it never claimed; got %v", err)
	}
	if !strings.Contains(rendered, "REPLAYED") {
		t.Errorf("expected the trace to keep reporting the replay to the operator, got:\n%s", rendered)
	}
	if strings.Contains(rendered, "LIVE (this build invokes make check)") {
		t.Errorf("a run whose stage BuildKit replayed must not report it as LIVE, whatever it declared; got:\n%s", rendered)
	}
}

// Output that never mentioned the test stage is an absence of evidence, not a
// replay. A build that failed before reaching the stage, or a stream this parser
// cannot read, must not be refused on a guess -- the same best-effort contract
// the rest of the stream's parsers hold to.
func TestGateBuildAcceptsAStageTheBuilderSaidNothingAbout(t *testing.T) {
	if _, err := runGateBuildThroughTheUmbrella(t, gateBuildFixture("erun-devops"), "", true); err != nil {
		t.Fatalf("expected a gate build with no evidence about its test stage to proceed rather than be refused on a guess, got %v", err)
	}
}
