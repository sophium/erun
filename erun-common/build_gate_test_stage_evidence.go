package eruncommon

import (
	"strings"
	"sync"
	"time"
)

// build_gate_test_stage_evidence.go answers one question about a finished
// `docker build`: did this run actually execute the Dockerfile's own gate?
//
// gateTestStageProvenanceLines has always had two answers -- a promoted image
// (CACHED) and everything else (LIVE) -- and the second is a claim the resolved
// plan cannot support. The per-Dockerfile guard (dockerfileHasGateTestStage,
// applyIncrementalPromotion) keeps a gate image out of the *fingerprint
// promotion* path, so the plan always says a real `docker build` will run; but
// BuildKit has a second cache underneath that one, and when the tree is
// unchanged it serves every instruction of the test stage from its own layer
// cache. The build then finishes in seconds at zero CPU, `make check` never
// executes, and the run reports the same LIVE line -- and the same exit 0 -- as
// one that spent four minutes gating the tree.
//
// Only the builder can tell those two apart, and it already does: BuildKit's
// captured `--progress=plain` stream prints CACHED for a step it served from
// cache and DONE <duration> for one it executed. That is the signal a warm
// cache cannot fake. An in-image marker is replayed verbatim along with the
// layer holding it, so it proves nothing about this run; the builder's own
// statement about what it did is the evidence, which is why none of this is
// asserted from inside the Dockerfile.

// gateStageName is the stage a gate Dockerfile declares its gate in. It is the
// same name dockerfileHasGateTestStage matches (`AS test`) and the same name
// BuildKit labels that stage's steps with, so the evidence below and the
// promotion guard cannot drift on which stage they mean.
const gateStageName = "test"

// gateStageExecution is what one real `docker build` did with the instruction
// steps of a Dockerfile's `test` stage, as its own captured stream reported it.
type gateStageExecution struct {
	// observed counts the stage's instruction steps this build's output
	// described at all. Zero means the stream said nothing about the stage -- a
	// build that failed before reaching it, or output this parser cannot read --
	// which is an absence of evidence, never evidence of a replay.
	observed int
	// executed counts those BuildKit ran in this build.
	executed int
	// replayed counts those it served from its layer cache, and so did not run.
	replayed int
}

// replayedWholeStage reports whether every instruction step this build's output
// described came from the layer cache, i.e. the stage did no work in this run.
//
// "Every", not "any": a stage whose unchanged upper layers were cached while
// the gate itself ran live is the ordinary case for a one-file change, and
// reporting that as a replay would be a false alarm on the exact builds the
// gate exists to measure. observed > 0 is required for the same reason in the
// other direction -- output that never mentioned the stage is not a replay.
func (e gateStageExecution) replayedWholeStage() bool {
	return e.observed > 0 && e.executed == 0 && e.replayed > 0
}

// gateTestStageExecutionInBuildOutput reads one real `docker build`'s captured
// `--progress=plain` stream and reports what this run did with the instruction
// steps of the Dockerfile's `test` stage.
//
// It never errors: output it cannot read yields an execution with nothing
// observed, which callers must treat as "no evidence" rather than as a replay,
// the same best-effort contract the rest of this stream's parsers hold to.
func gateTestStageExecutionInBuildOutput(output string) gateStageExecution {
	order, vertices := parseBuildKitVertices(output)
	var execution gateStageExecution
	for _, id := range order {
		vertex := vertices[id]
		if vertex.stage != gateStageName || gateStageBaseStep(vertex.label) {
			continue
		}
		execution.observed++
		switch {
		case vertex.hasDone:
			// A step carrying both lines was executed; only CACHED without DONE
			// is proof this run did not run it.
			execution.executed++
		case vertex.cached:
			execution.replayed++
		}
	}
	return execution
}

// gateStageBaseStep reports whether a step is its stage's `FROM` -- the step
// that resolves the stage's base image rather than doing the stage's own work.
//
// Leaving it out is not a detail, it is what makes a replay detectable at all:
// BuildKit reports that step DONE even when it served the whole stage from
// cache. Measured against a Dockerfile whose test stage is wholly cached, the
// stream reads
//
//	#4 [test 1/4] FROM docker.io/library/alpine:3.20@sha256:d9e85...
//	#4 DONE 0.0s
//	#5 [test 2/4] WORKDIR /src
//	#5 CACHED
//	...every instruction beneath it CACHED...
//
// -- the base image is re-verified cheaply however little needs rebuilding.
// Counting it would leave a wholly replayed stage looking like one step of real
// work and hide the very outcome this file exists to report.
func gateStageBaseStep(label string) bool {
	fields := strings.Fields(label)
	return len(fields) > 0 && strings.EqualFold(fields[0], "from")
}

// gateTestStageProvenance is the run-scoped record of what each gate image's
// real `docker build` did with its Dockerfile's test stage. It is filled in as
// each image and platform finishes and read once, by the build umbrella, to
// report the outcome. It is mutex-guarded because a run builds images
// concurrently (see RunDockerBuilds) and a multi-platform build reports once
// per platform.
type gateTestStageProvenance struct {
	mu  sync.Mutex
	run map[string]gateStageExecution
}

func newGateTestStageProvenance() *gateTestStageProvenance {
	return &gateTestStageProvenance{run: make(map[string]gateStageExecution)}
}

// record folds one platform's finished build into the entry for an image tag.
// Platforms merge rather than replace: a stage that executed on any one of them
// executed in this run, so an entry can report a replay only when every
// platform's build replayed it.
func (p *gateTestStageProvenance) record(tag string, execution gateStageExecution) {
	if p == nil {
		return
	}
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	merged := p.run[tag]
	merged.observed += execution.observed
	merged.executed += execution.executed
	merged.replayed += execution.replayed
	p.run[tag] = merged
}

// execution returns what this run observed for one image tag, and whether it
// has an entry for it at all. A tag no build reported on -- a dry run, an image
// whose Dockerfile is not a gate, a build that failed before its docker build
// -- has no entry, which callers read as no evidence rather than as a replay.
func (p *gateTestStageProvenance) execution(tag string) (gateStageExecution, bool) {
	if p == nil {
		return gateStageExecution{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	execution, ok := p.run[strings.TrimSpace(tag)]
	return execution, ok
}

// withGateStageEvidence chains this run's gate evidence onto an existing
// per-platform observer, so the evidence rides the same callback the timing
// collector already receives rather than needing a second seam on
// DockerBuildSpec. The captured build stream is what PlatformObserver is handed
// for exactly this kind of mining, and re-reading it for the stage's own
// provenance costs one pass over output the build already produced.
//
// It returns the observer unchanged for a build that is not a gate, so an
// ordinary image's callback and cost are untouched.
func (p *gateTestStageProvenance) withGateStageEvidence(buildInput DockerBuildSpec, observer func(string, time.Duration, error, *BuildCgroupMetrics, string)) func(string, time.Duration, error, *BuildCgroupMetrics, string) {
	if p == nil || !buildInput.GateTestStage {
		return observer
	}
	tag := strings.TrimSpace(buildInput.Image.Tag)
	if tag == "" {
		return observer
	}
	return func(platform string, elapsed time.Duration, err error, cgroup *BuildCgroupMetrics, buildOutput string) {
		observer(platform, elapsed, err, cgroup, buildOutput)
		// A failed build reports nothing: its stage may or may not have been
		// reached, and the run is already failing with the builder's own reason.
		// A second, unverified story attached to that failure would only be noise.
		if err != nil {
			return
		}
		p.record(tag, gateTestStageExecutionInBuildOutput(buildOutput))
	}
}
