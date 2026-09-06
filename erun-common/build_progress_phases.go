package eruncommon

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A build's per-platform timing step used to bottom out at one duration, no
// matter how long the underlying `docker build` ran. On a gate build where
// one image is 97-99% of total wall clock, that single number cannot say
// which part of the build was slow. BuildKit's own `--progress=plain` output
// (already captured by runDockerBuildOnce, never discarded) already answers
// that question at two levels of granularity: which Dockerfile step ran how
// long, and — for the `RUN make check` step specifically — which of the
// Makefile's own `>> <phase>` markers ran how long inside it. Both levels are
// parsed here and attached as timing children under the per-platform step.

// buildkitVertexStartPattern matches BuildKit's per-step start line, e.g.
//
//	#7 [4/8] RUN make check
//	#3 [internal] load metadata for docker.io/library/golang:1.26.0
//
// The bracketed field is the stage index for a real Dockerfile instruction,
// or a fixed keyword for BuildKit's own internal steps; either way, what
// follows it is the step's own command.
var buildkitVertexStartPattern = regexp.MustCompile(`^#(\d+) \[([^\]]*)\] (.*)$`)

// buildkitVertexDonePattern matches BuildKit's per-step completion line, e.g.
//
//	#7 DONE 1823.99s
//
// A step that never emits this line (a cache hit reported as `#7 CACHED`, or
// one BuildKit never finished) has no known duration and is left out of the
// phase tree rather than reported as zero.
var buildkitVertexDonePattern = regexp.MustCompile(`^#(\d+) DONE ([\d.]+)s$`)

// buildProgressPhasesTopN bounds how many phase rows attach at one level of
// the tree — a Dockerfile can have dozens of steps, and a `make check` run
// dozens of `>> phase` markers. Rows beyond the costliest N fold into one
// synthetic "smaller steps" row (same shape as timing.go's own
// "(unaccounted)"/"(ran concurrently)" synthetic rows) so nothing is silently
// dropped from the total, but the tree stays a summary rather than a log.
const buildProgressPhasesTopN = 15

// buildProgressConcurrentSentinel is the marker `make check` prints before it
// fans check-gate out with `-j`. Phase spans are derived from the gap between
// one `>> phase` line and the next, which measures that phase's own work only
// while the step runs its phases one at a time. Once several targets
// interleave their output, a span covers whatever else was running too, so
// the rows below such a step are elapsed windows rather than per-phase cost.
// The recipe announces the fan-out rather than the parser guessing at it: a
// heuristic on the step command would silently go wrong the next time a
// recipe changes how it parallelises.
const buildProgressConcurrentSentinel = "concurrent-phase-spans"

// buildProgressConcurrentSuffix marks a row whose duration is an elapsed
// window. Callers that sum these rows would otherwise read them as exclusive
// costs and conclude the wrong step is expensive.
const buildProgressConcurrentSuffix = " (elapsed window, concurrent)"

// buildProgressPhase is one node of the parsed phase tree, before it is
// attached to the real *stepTiming tree via addFinishedChild.
type buildProgressPhase struct {
	name     string
	duration time.Duration
	children []buildProgressPhase
}

// buildKitVertex accumulates everything parseBuildKitVertices learns about
// one numbered BuildKit step across the whole captured output.
type buildKitVertex struct {
	label    string
	hasDone  bool
	doneSecs float64
	markers  []buildKitPhaseMarker
}

// buildKitPhaseMarker is one `>> <phase>` line a step printed, tagged with
// the elapsed-seconds offset BuildKit prefixed it with.
type buildKitPhaseMarker struct {
	offsetSecs float64
	name       string
}

// buildKitProgressPhases parses a captured `docker build --progress=plain`
// stream into the bounded phase tree described above. It never errors: a
// malformed or unrecognisable stream simply yields no phases, the same
// best-effort contract the rest of the timing collector holds to.
func buildKitProgressPhases(output string) []buildProgressPhase {
	order, vertices := parseBuildKitVertices(output)
	phases := make([]buildProgressPhase, 0, len(order))
	for _, id := range order {
		v := vertices[id]
		if !v.hasDone {
			continue
		}
		phase := buildProgressPhase{name: dockerBuildPhaseLabel(v.label), duration: durationFromSeconds(v.doneSecs)}
		if len(v.markers) > 0 {
			phase.children = boundProgressPhases(markerPhases(v.markers, v.doneSecs))
		}
		phases = append(phases, phase)
	}
	return boundProgressPhases(phases)
}

// parseBuildKitVertices walks the captured output once, returning every
// numbered step in first-seen order alongside what is known about it. A step
// with no start line (BuildKit always emits one before anything else about
// that step) is never recorded, so a DONE or output line for an unknown id is
// silently ignored rather than fabricating a step for it. The three line
// shapes are mutually exclusive (a start line's bracketed stage index, a DONE
// line's literal "DONE", and an output line's decimal offset can never match
// the same line), so every line is offered to all three in turn rather than
// short-circuiting on the first match.
func parseBuildKitVertices(output string) (order []string, vertices map[string]*buildKitVertex) {
	vertices = map[string]*buildKitVertex{}
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimRight(raw, "\r")
		if id, isNew := applyVertexStart(line, vertices); isNew {
			order = append(order, id)
		}
		applyVertexDone(line, vertices)
		applyPhaseMarker(line, vertices)
	}
	return order, vertices
}

// applyVertexStart registers a vertex the first time its start line appears,
// reporting isNew only that first time so the caller appends to `order`
// exactly once per vertex.
func applyVertexStart(line string, vertices map[string]*buildKitVertex) (id string, isNew bool) {
	match := buildkitVertexStartPattern.FindStringSubmatch(line)
	if match == nil {
		return "", false
	}
	id = match[1]
	if _, exists := vertices[id]; exists {
		return id, false
	}
	vertices[id] = &buildKitVertex{label: strings.TrimSpace(match[3])}
	return id, true
}

// applyVertexDone records a step's completion duration when its DONE line
// names a vertex already seen via a start line.
func applyVertexDone(line string, vertices map[string]*buildKitVertex) {
	match := buildkitVertexDonePattern.FindStringSubmatch(line)
	if match == nil {
		return
	}
	v, ok := vertices[match[1]]
	if !ok {
		return
	}
	secs, err := strconv.ParseFloat(match[2], 64)
	if err != nil {
		return
	}
	v.hasDone = true
	v.doneSecs = secs
}

// applyPhaseMarker records one `>> <phase>` line a step printed.
// buildkitStepOutputPattern (build_failure_reason.go) matches any line a step
// printed; only the Makefile's own phase markers are structured enough here
// to become sub-phases.
func applyPhaseMarker(line string, vertices map[string]*buildKitVertex) {
	match := buildkitStepOutputPattern.FindStringSubmatch(line)
	if match == nil {
		return
	}
	v, ok := vertices[match[1]]
	if !ok {
		return
	}
	text := strings.TrimSpace(match[3])
	if !strings.HasPrefix(text, ">> ") {
		return
	}
	offset, err := strconv.ParseFloat(match[2], 64)
	if err != nil {
		return
	}
	v.markers = append(v.markers, buildKitPhaseMarker{offsetSecs: offset, name: strings.TrimPrefix(text, ">> ")})
}

// markerPhases turns one step's ordered `>> phase` markers into sub-phases,
// each running from its own offset to the next marker's (or, for the last
// one, to the step's own DONE time).
func markerPhases(markers []buildKitPhaseMarker, vertexDoneSecs float64) []buildProgressPhase {
	concurrent := false
	kept := make([]buildKitPhaseMarker, 0, len(markers))
	for _, m := range markers {
		if strings.HasPrefix(m.name, buildProgressConcurrentSentinel) {
			concurrent = true
			continue
		}
		kept = append(kept, m)
	}
	markers = kept
	sorted := make([]buildKitPhaseMarker, len(markers))
	copy(sorted, markers)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].offsetSecs < sorted[j].offsetSecs })

	phases := make([]buildProgressPhase, 0, len(sorted))
	for i, marker := range sorted {
		end := vertexDoneSecs
		if i+1 < len(sorted) {
			end = sorted[i+1].offsetSecs
		}
		dur := end - marker.offsetSecs
		if dur < 0 {
			dur = 0
		}
		name := marker.name
		if concurrent {
			name += buildProgressConcurrentSuffix
		}
		phases = append(phases, buildProgressPhase{name: name, duration: durationFromSeconds(dur)})
	}
	return phases
}

// boundProgressPhases keeps only the costliest buildProgressPhasesTopN
// entries at one level, in their original relative order, and folds the rest
// into one synthetic trailing row — mirroring timing.go's own
// "(unaccounted)"/"(ran concurrently)" synthetic rows, which likewise report
// a real total rather than dropping it.
func boundProgressPhases(phases []buildProgressPhase) []buildProgressPhase {
	if len(phases) <= buildProgressPhasesTopN {
		return phases
	}
	rank := make([]int, len(phases))
	for i := range rank {
		rank[i] = i
	}
	sort.SliceStable(rank, func(i, j int) bool { return phases[rank[i]].duration > phases[rank[j]].duration })
	keep := make(map[int]bool, buildProgressPhasesTopN)
	for _, idx := range rank[:buildProgressPhasesTopN] {
		keep[idx] = true
	}

	kept := make([]buildProgressPhase, 0, buildProgressPhasesTopN+1)
	var restTotal time.Duration
	restCount := 0
	for i, phase := range phases {
		if keep[i] {
			kept = append(kept, phase)
			continue
		}
		restTotal += phase.duration
		restCount++
	}
	kept = append(kept, buildProgressPhase{
		name:     "(" + strconv.Itoa(restCount) + " smaller steps)",
		duration: restTotal,
	})
	return kept
}

func durationFromSeconds(seconds float64) time.Duration {
	if seconds < 0 {
		seconds = 0
	}
	return time.Duration(seconds * float64(time.Second))
}

// dockerBuildPhaseLabel trims a Dockerfile step's own command down to
// something a table row can show, the same truncate-a-long-line treatment
// dockerBuildFailureReason gives a failing step's last words.
func dockerBuildPhaseLabel(label string) string {
	const maxLabelLength = 80
	label = strings.TrimSpace(label)
	if len(label) <= maxLabelLength {
		return label
	}
	return strings.TrimSpace(label[:maxLabelLength]) + "…"
}
