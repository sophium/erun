package core

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Per-step and `>> <phase>` timings parsed from BuildKit's --progress=plain output.

// buildkitVertexStartPattern matches a step start line, e.g. `#7 [4/8] RUN go test ./...`.
var buildkitVertexStartPattern = regexp.MustCompile(`^#(\d+) \[([^\]]*)\] (.*)$`)

// buildkitVertexDonePattern matches `#7 DONE 1823.99s`. A step without it (cached
// or unfinished) has no duration and is omitted, not reported as zero.
var buildkitVertexDonePattern = regexp.MustCompile(`^#(\d+) DONE ([\d.]+)s$`)

// BuildProgressPhasesTopN bounds rows per level; the rest fold into one "smaller
// steps" row so the total is preserved.
const BuildProgressPhasesTopN = 15

// buildProgressConcurrentSentinel is printed by a step that runs its phases
// concurrently, so its marker spans are elapsed windows, not per-phase cost.
const buildProgressConcurrentSentinel = "concurrent-phase-spans"

// BuildProgressConcurrentSuffix marks an elapsed-window row; summing such rows
// as exclusive costs is wrong.
const BuildProgressConcurrentSuffix = " (elapsed window, concurrent)"

// buildProgressSelfTimedPattern matches a marker carrying its own duration, e.g.
// `>> golangci-lint erun-ui [83s]`. Replayed output makes spans meaningless, so this wins.
var buildProgressSelfTimedPattern = regexp.MustCompile(`^(.*) \[([0-9]+)s\]$`)

type BuildProgressPhase struct {
	Name     string
	Duration time.Duration
	Children []BuildProgressPhase
}

type buildKitVertex struct {
	label    string
	hasDone  bool
	doneSecs float64
	markers  []BuildKitPhaseMarker
}

type BuildKitPhaseMarker struct {
	OffsetSecs float64
	Name       string
}

// BuildKitProgressPhases parses a --progress=plain stream into the bounded phase
// tree. It never errors; an unrecognised stream yields no phases.
func BuildKitProgressPhases(output string) []BuildProgressPhase {
	order, vertices := parseBuildKitVertices(output)
	phases := make([]BuildProgressPhase, 0, len(order))
	for _, id := range order {
		v := vertices[id]
		if !v.hasDone {
			continue
		}
		phase := BuildProgressPhase{Name: dockerBuildPhaseLabel(v.label), Duration: durationFromSeconds(v.doneSecs)}
		if len(v.markers) > 0 {
			phase.Children = boundProgressPhases(MarkerPhases(v.markers, v.doneSecs))
		}
		phases = append(phases, phase)
	}
	return boundProgressPhases(phases)
}

// parseBuildKitVertices returns steps in first-seen order. Lines for an id with
// no start line are ignored.
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

func applyPhaseMarker(line string, vertices map[string]*buildKitVertex) {
	match := BuildkitStepOutputPattern.FindStringSubmatch(line)
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
	v.markers = append(v.markers, BuildKitPhaseMarker{OffsetSecs: offset, Name: strings.TrimPrefix(text, ">> ")})
}

// MarkerPhases spans each marker to the next one, the last to the step's DONE time.
func MarkerPhases(markers []BuildKitPhaseMarker, vertexDoneSecs float64) []BuildProgressPhase {
	concurrent := false
	kept := make([]BuildKitPhaseMarker, 0, len(markers))
	for _, m := range markers {
		if strings.HasPrefix(m.Name, buildProgressConcurrentSentinel) {
			concurrent = true
			continue
		}
		kept = append(kept, m)
	}
	markers = kept
	sorted := make([]BuildKitPhaseMarker, len(markers))
	copy(sorted, markers)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].OffsetSecs < sorted[j].OffsetSecs })

	phases := make([]BuildProgressPhase, 0, len(sorted))
	for i, marker := range sorted {
		end := vertexDoneSecs
		if i+1 < len(sorted) {
			end = sorted[i+1].OffsetSecs
		}
		dur := end - marker.OffsetSecs
		if dur < 0 {
			dur = 0
		}
		name := marker.Name
		if m := buildProgressSelfTimedPattern.FindStringSubmatch(name); m != nil {
			if secs, err := strconv.ParseFloat(m[2], 64); err == nil {
				phases = append(phases, BuildProgressPhase{Name: m[1], Duration: durationFromSeconds(secs)})
				continue
			}
		}
		if concurrent {
			name += BuildProgressConcurrentSuffix
		}
		phases = append(phases, BuildProgressPhase{Name: name, Duration: durationFromSeconds(dur)})
	}
	return phases
}

// boundProgressPhases keeps the costliest buildProgressPhasesTopN in original order
// and folds the rest into one trailing row.
func boundProgressPhases(phases []BuildProgressPhase) []BuildProgressPhase {
	if len(phases) <= BuildProgressPhasesTopN {
		return phases
	}
	rank := make([]int, len(phases))
	for i := range rank {
		rank[i] = i
	}
	sort.SliceStable(rank, func(i, j int) bool { return phases[rank[i]].Duration > phases[rank[j]].Duration })
	keep := make(map[int]bool, BuildProgressPhasesTopN)
	for _, idx := range rank[:BuildProgressPhasesTopN] {
		keep[idx] = true
	}

	kept := make([]BuildProgressPhase, 0, BuildProgressPhasesTopN+1)
	var restTotal time.Duration
	restCount := 0
	for i, phase := range phases {
		if keep[i] {
			kept = append(kept, phase)
			continue
		}
		restTotal += phase.Duration
		restCount++
	}
	kept = append(kept, BuildProgressPhase{
		Name:     "(" + strconv.Itoa(restCount) + " smaller steps)",
		Duration: restTotal,
	})
	return kept
}

func durationFromSeconds(seconds float64) time.Duration {
	if seconds < 0 {
		seconds = 0
	}
	return time.Duration(seconds * float64(time.Second))
}

func dockerBuildPhaseLabel(label string) string {
	const maxLabelLength = 80
	label = strings.TrimSpace(label)
	if len(label) <= maxLabelLength {
		return label
	}
	return strings.TrimSpace(label[:maxLabelLength]) + "…"
}
