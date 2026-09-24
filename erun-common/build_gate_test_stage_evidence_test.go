package eruncommon

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// The captures below are verbatim `docker build --progress=plain` output from
// real builds of a Dockerfile shaped like the gate convention's (`FROM … AS
// test` with a later `COPY --from=test`), not hand-written paraphrases. The
// whole parsing lives on details of that stream that are easy to get wrong from
// memory -- the stage name riding in the bracket, and the stage's own `FROM`
// step being reported DONE even when nothing else in the stage ran -- so the
// tests that decide a build was replayed are pinned against output the builder
// actually produced.

// cachedTestStageBuildOutput is the second, unchanged-tree build: every
// instruction of the test stage was served from BuildKit's layer cache, so
// `make check` never executed, while the stage's FROM step still reports DONE.
const cachedTestStageBuildOutput = `#0 building with "default" instance using docker driver

#1 [internal] load build definition from Dockerfile
#1 transferring dockerfile: 270B done
#1 DONE 0.0s

#2 [internal] load metadata for docker.io/library/alpine:3.20
#2 DONE 0.2s

#3 [internal] load .dockerignore
#3 transferring context: 2B done
#3 DONE 0.0s

#4 [test 1/4] FROM docker.io/library/alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
#4 DONE 0.0s

#5 [builder 2/3] COPY --from=test /test-ok /tmp/erun-test-ok
#5 CACHED

#6 [test 2/4] WORKDIR /src
#6 CACHED

#7 [test 3/4] RUN echo building-thing > /build-marker
#7 CACHED

#8 [test 4/4] RUN make check && touch /test-ok
#8 CACHED

#9 [builder 3/3] RUN echo done
#9 CACHED

#10 exporting to image
#10 exporting layers done
#10 writing image sha256:8950d6f896bee4929bcde764295929e00906f9ecf15467f9a6feb3e215712403 done
#10 DONE 0.1s
`

// liveTestStageBuildOutput is the first, cold build of the same Dockerfile:
// every instruction of the test stage ran.
const liveTestStageBuildOutput = `#4 [test 1/4] FROM docker.io/library/alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
#4 DONE 0.3s

#5 [test 2/4] WORKDIR /src
#5 DONE 6.7s

#6 [test 3/4] RUN echo building-thing > /build-marker
#6 DONE 0.7s

#7 [test 4/4] RUN make check && touch /test-ok
#7 0.391 make check
#7 DONE 239.9s

#8 [builder 2/3] COPY --from=test /test-ok /tmp/erun-test-ok
#8 DONE 0.2s
`

// partialTestStageBuildOutput is a one-source-file change: the test stage's
// unchanged early steps came back CACHED while the COPY that carries the
// change, and the gate instruction after it, both ran. The stage did run.
const partialTestStageBuildOutput = `#4 [test 1/5] FROM docker.io/library/alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
#4 DONE 0.0s

#6 [test 2/5] WORKDIR /src
#6 CACHED

#7 [test 3/5] RUN echo stable-layer > /stable
#7 CACHED

#8 [test 4/5] COPY src /src/src
#8 DONE 0.1s

#9 [test 5/5] RUN make check && touch /test-ok
#9 DONE 0.8s
`

// gateBuildFixture is one gate image, tagged the way the real devops image is.
func gateBuildFixture(tag string) DockerBuildSpec {
	return DockerBuildSpec{
		GateTestStage: true,
		Image:         DockerImageReference{ImageName: "erun-devops", Tag: tag},
		Platforms:     []string{"linux/amd64"},
	}
}

// runGateBuildThroughTheRealPath drives one gate image through the same two
// functions a real `erun build` does -- the umbrella that brackets the run and
// reports the provenance, and the execution that runs the build -- with a
// builder stub standing in for docker and handing back the captured BuildKit
// stream a real one would.
//
// It returns the run's trace and the run's own outcome, because those are now two
// different questions and both are asserted: a wholly replayed test stage is
// refused (the umbrella's error) as well as reported (the trace), while a stage
// that executed is neither.
func runGateBuildThroughTheRealPath(t *testing.T, build DockerBuildSpec, buildOutput string) (string, error) {
	t.Helper()
	var log bytes.Buffer
	ctx, finish := traceBuildUmbrella(Context{Logger: NewLoggerWithWriters(VerbosityInfo, &log, &log)}, []DockerBuildSpec{build})

	buildErr := RunDockerBuild(ctx, build, func(input DockerBuildSpec, stdout, stderr io.Writer) error {
		if input.PlatformObserver == nil {
			t.Fatal("expected the build to carry a platform observer, as every real build does")
		}
		input.PlatformObserver("linux/amd64", 2*time.Second, nil, nil, buildOutput)
		return nil
	})
	if buildErr != nil {
		t.Fatalf("the stub build succeeded, so RunDockerBuild must not fail: %v", buildErr)
	}
	finish(&buildErr)
	return log.String(), buildErr
}

// TestGateBuildWhoseTestStageTheLayerCacheReplayedDoesNotReportLive reproduces
// the reported defect: a gate build whose Dockerfile's test stage BuildKit
// served entirely from its own layer cache -- `make check` never executed --
// reported the same `test stage (…): LIVE (this build invokes make check)` line,
// and the same exit 0, as a build that really ran the gate.
//
// Before this change the trace said LIVE here, because the per-Dockerfile guard
// had already done its job: the image was not promoted from a cached fingerprint
// image, so the plan said a real docker build would run, and nothing below the
// plan could see that BuildKit answered it from the second cache underneath.
func TestGateBuildWhoseTestStageTheLayerCacheReplayedDoesNotReportLive(t *testing.T) {
	rendered, err := runGateBuildThroughTheRealPath(t, gateBuildFixture("ghcr.io/sophium/erun-devops:1.0.284"), cachedTestStageBuildOutput)

	// The exit status is the half the merge queue reads, so the replay has to
	// reach it and not only the trace.
	if !errors.Is(err, ErrGateTestStageReplayed) {
		t.Errorf("expected the replayed test stage to refuse the run itself, not only to be reported; got %T: %v", err, err)
	}
	if strings.Contains(rendered, "LIVE") {
		t.Errorf("a build whose test stage BuildKit replayed must not report the gate as LIVE anywhere in its trace; got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "REPLAYED") {
		t.Errorf("expected the trace to report the replayed test stage rather than an unqualified success; got:\n%s", rendered)
	}
}

// TestGateBuildWhoseTestStageRanStillReportsLive is the other half of the
// reproduction: the new outcome must not swallow the ordinary one. A build that
// really executed the gate still says LIVE, and must not be labelled a replay.
func TestGateBuildWhoseTestStageRanStillReportsLive(t *testing.T) {
	rendered, err := runGateBuildThroughTheRealPath(t, gateBuildFixture("ghcr.io/sophium/erun-devops:1.0.284"), liveTestStageBuildOutput)
	if err != nil {
		t.Fatalf("a build that executed its test stage must not be refused: %v", err)
	}

	if !strings.Contains(rendered, "LIVE (this build invokes make check)") {
		t.Errorf("a build that executed its test stage must still report LIVE; got:\n%s", rendered)
	}
	if strings.Contains(rendered, "REPLAYED") {
		t.Errorf("a build that executed its test stage is not a replay; got:\n%s", rendered)
	}
}

// TestGateBuildWithOnlyItsEarlyTestStageStepsCachedStillReportsLive guards the
// over-eager version of this detection. A one-file change caches a test stage's
// unchanged early steps while the gate instruction itself runs live; a rule that
// called any cached step a replay would report the exact builds the gate exists
// to measure as unrun.
func TestGateBuildWithOnlyItsEarlyTestStageStepsCachedStillReportsLive(t *testing.T) {
	rendered, err := runGateBuildThroughTheRealPath(t, gateBuildFixture("ghcr.io/sophium/erun-devops:1.0.284"), partialTestStageBuildOutput)
	if err != nil {
		t.Fatalf("a test stage whose gate step ran must not be refused: %v", err)
	}

	if !strings.Contains(rendered, "LIVE (this build invokes make check)") {
		t.Errorf("a test stage whose gate step ran is live however many steps above it were cached; got:\n%s", rendered)
	}
	if strings.Contains(rendered, "REPLAYED") {
		t.Errorf("a partially cached test stage is not a whole-stage replay; got:\n%s", rendered)
	}
}

// TestGateTestStageExecutionIgnoresTheStageBaseStep pins the detail that makes a
// replay detectable at all. BuildKit reports a stage's `FROM` step DONE even
// when it served every instruction below it from cache, so a rule that counted
// it would see one step of real work in a wholly replayed stage and never
// report the replay.
func TestGateTestStageExecutionIgnoresTheStageBaseStep(t *testing.T) {
	execution := gateTestStageExecutionInBuildOutput(cachedTestStageBuildOutput)
	if execution.executed != 0 {
		t.Errorf("no instruction step of this test stage executed, so executed must be 0; got %+v", execution)
	}
	if !execution.replayedWholeStage() {
		t.Errorf("expected the wholly cached test stage to read as replayed; got %+v", execution)
	}
}

func TestGateTestStageExecutionCountsWhatTheStageDid(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		observed int
		executed int
		replayed int
		whole    bool
	}{
		{
			name:     "wholly cached",
			output:   cachedTestStageBuildOutput,
			observed: 3,
			replayed: 3,
			whole:    true,
		},
		{
			name:     "wholly live",
			output:   liveTestStageBuildOutput,
			observed: 3,
			executed: 3,
		},
		{
			name:     "partially cached",
			output:   partialTestStageBuildOutput,
			observed: 4,
			executed: 2,
			replayed: 2,
		},
		{
			// Not a replay: the builder said nothing about the stage. An
			// absence of evidence must not be reported as one.
			name:   "unrecognisable output",
			output: "just some chatter\nand more chatter\n",
		},
		{
			// A Dockerfile with no `test` stage at all.
			name:   "another stage's steps",
			output: "#1 [builder 1/2] RUN go build ./...\n#1 DONE 12.5s\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			execution := gateTestStageExecutionInBuildOutput(test.output)
			if execution.observed != test.observed || execution.executed != test.executed || execution.replayed != test.replayed {
				t.Errorf("execution = %+v, want observed=%d executed=%d replayed=%d", execution, test.observed, test.executed, test.replayed)
			}
			if execution.replayedWholeStage() != test.whole {
				t.Errorf("replayedWholeStage() = %v, want %v (execution: %+v)", execution.replayedWholeStage(), test.whole, execution)
			}
		})
	}
}

// TestGateTestStageProvenanceWithoutEvidenceKeepsTheLiveWording pins the
// no-evidence case: a run whose builds reported nothing -- a dry run, a build
// that failed before reaching the stage, output this parser cannot read -- must
// keep the existing declaration rather than being guessed at as a replay.
func TestGateTestStageProvenanceWithoutEvidenceKeepsTheLiveWording(t *testing.T) {
	build := gateBuildFixture("ghcr.io/sophium/erun-devops:1.0.284")

	for _, evidence := range []*gateTestStageProvenance{nil, newGateTestStageProvenance()} {
		lines := gateTestStageProvenanceLines([]DockerBuildSpec{build}, evidence)
		if len(lines) != 1 {
			t.Fatalf("expected exactly one provenance line, got %v", lines)
		}
		if !strings.Contains(lines[0], "LIVE") {
			t.Errorf("expected the LIVE wording without evidence, got: %s", lines[0])
		}
	}

	empty := newGateTestStageProvenance()
	empty.record("ghcr.io/sophium/erun-devops:1.0.284", gateStageExecution{})
	lines := gateTestStageProvenanceLines([]DockerBuildSpec{build}, empty)
	if !strings.Contains(lines[0], "LIVE") {
		t.Errorf("an entry with nothing observed is not a replay, got: %s", lines[0])
	}
}

// TestGateTestStagePlanLinesDoNotUseTheOutcomeVocabulary is what keeps the
// replayed and live states distinct in the trace. The plan line is emitted
// before the run, when the outcome is not yet knowable; if it used LIVE, a
// replayed build's log would still contain the word a real gate run prints, and
// grepping the trace for the gate's verdict would find it.
func TestGateTestStagePlanLinesDoNotUseTheOutcomeVocabulary(t *testing.T) {
	lines := gateTestStagePlanLines([]DockerBuildSpec{gateBuildFixture("ghcr.io/sophium/erun-devops:1.0.284")})
	if len(lines) != 1 {
		t.Fatalf("expected exactly one plan line, got %v", lines)
	}
	for _, outcome := range []string{"LIVE", "CACHED", "REPLAYED"} {
		if strings.Contains(lines[0], outcome) {
			t.Errorf("the plan line must not claim the %s outcome, which only the builder can report; got: %s", outcome, lines[0])
		}
	}
}

// TestGateTestStagePlanLinesSkipAPromotedGateBuild: a promoted gate build never
// reaches a docker build, so there is no plan-time gate event to announce and
// the outcome line carries the whole story.
func TestGateTestStagePlanLinesSkipAPromotedGateBuild(t *testing.T) {
	promoted := gateBuildFixture("ghcr.io/sophium/erun-devops:1.0.284")
	promoted.Promote = true

	if lines := gateTestStagePlanLines([]DockerBuildSpec{promoted}); len(lines) != 0 {
		t.Errorf("expected no plan line for a promoted gate build, got %v", lines)
	}
	lines := gateTestStageProvenanceLines([]DockerBuildSpec{promoted}, nil)
	if len(lines) != 1 || !strings.Contains(lines[0], "CACHED") {
		t.Errorf("expected the outcome line to report the promoted gate build as CACHED, got %v", lines)
	}
}

// TestGateTestStageExecutionIsEmptyForOutputWithoutTheStage keeps the
// ordinary case cheap: a gate image's stream that never mentions a `test` stage
// yields no evidence at all, so nothing downstream can read it as a replay.
func TestGateTestStageExecutionIsEmptyForOutputWithoutTheStage(t *testing.T) {
	execution := gateTestStageExecutionInBuildOutput("#1 [builder 1/2] RUN go build ./...\n#1 DONE 12.5s\n")
	if execution.observed != 0 || execution.replayedWholeStage() {
		t.Errorf("output with no test stage must yield no evidence, got %+v", execution)
	}
}

// TestGateTestStageProvenanceMergesConcurrentPlatforms covers the concurrency
// the recorder exists to survive: a multi-platform build reports once per
// platform, potentially at the same time, and a stage that executed on any one
// platform executed in this run.
func TestGateTestStageProvenanceMergesConcurrentPlatforms(t *testing.T) {
	const tag = "ghcr.io/sophium/erun-devops:1.0.284"
	evidence := newGateTestStageProvenance()

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			output := cachedTestStageBuildOutput
			if i%2 == 0 {
				output = liveTestStageBuildOutput
			}
			evidence.record(tag, gateTestStageExecutionInBuildOutput(output))
		}(i)
	}
	wg.Wait()

	execution, ok := evidence.execution(tag)
	if !ok {
		t.Fatal("expected an entry for the tag every platform reported against")
	}
	if execution.executed == 0 {
		t.Errorf("a stage that executed on some platforms must be recorded as executed; got %+v", execution)
	}
	if execution.replayedWholeStage() {
		t.Errorf("a stage that executed on any platform is not a whole-stage replay; got %+v", execution)
	}
}
