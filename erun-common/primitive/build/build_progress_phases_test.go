package build

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

func TestBuildKitProgressPhasesParsesDockerfileSteps(t *testing.T) {
	output := `#1 [internal] load build definition from Dockerfile
#1 DONE 0.0s
#2 [1/3] FROM docker.io/library/golang:1.26.0
#2 CACHED
#3 [2/3] COPY . .
#3 DONE 1.5s
#4 [3/3] RUN make check
#4 0.10 running tests
#4 DONE 200.25s`

	phases := core.BuildKitProgressPhases(output)
	if len(phases) != 3 {
		t.Fatalf("expected 3 phases (internal, COPY, RUN — the CACHED FROM has no DONE), got %d: %+v", len(phases), phases)
	}

	byName := map[string]core.BuildProgressPhase{}
	for _, p := range phases {
		byName[p.Name] = p
	}
	if _, ok := byName["FROM docker.io/library/golang:1.26.0"]; ok {
		t.Fatalf("a cached step with no DONE line must not be reported as a zero-duration phase, got: %+v", phases)
	}
	if got := byName["load build definition from Dockerfile"].Duration; got != 0 {
		t.Fatalf("expected 0s for the internal step, got %v", got)
	}
	if got := byName["COPY . ."].Duration; got != 1500*time.Millisecond {
		t.Fatalf("expected 1.5s for the COPY step, got %v", got)
	}
	if got := byName["RUN make check"].Duration; got != 200250*time.Millisecond {
		t.Fatalf("expected 200.25s for the RUN step, got %v", got)
	}
}

func TestBuildKitProgressPhasesNestsMakefilePhaseMarkersUnderTheirStep(t *testing.T) {
	// BuildKit prefixes each line with elapsed seconds; the Makefile's `>> <phase>` markers ride inside.
	output := `#8 [4/8] RUN make check
#8 0.12 >> golangci-lint erun-common
#8 5.20 >> golangci-lint erun-cli
#8 54.73 >> go test erun-ui
#8 90.20 >> go test erun-backend-api
#8 DONE 107.50s`

	phases := core.BuildKitProgressPhases(output)
	if len(phases) != 1 {
		t.Fatalf("expected the one RUN step, got %d: %+v", len(phases), phases)
	}
	step := phases[0]
	if step.Name != "RUN make check" || step.Duration != 107500*time.Millisecond {
		t.Fatalf("unexpected top-level step: %+v", step)
	}
	if len(step.Children) != 4 {
		t.Fatalf("expected 4 make-phase children, got %d: %+v", len(step.Children), step.Children)
	}

	want := []struct {
		name string
		dur  time.Duration
	}{
		{"golangci-lint erun-common", 5080 * time.Millisecond},
		{"golangci-lint erun-cli", (5473 - 520) * 10 * time.Millisecond},
		{"go test erun-ui", (9020 - 5473) * 10 * time.Millisecond},
		{"go test erun-backend-api", (10750 - 9020) * 10 * time.Millisecond},
	}
	for i, w := range want {
		if step.Children[i].Name != w.name {
			t.Fatalf("child %d: name = %q, want %q (children: %+v)", i, step.Children[i].Name, w.name, step.Children)
		}
		diff := step.Children[i].Duration - w.dur
		if diff < -time.Millisecond || diff > time.Millisecond {
			t.Fatalf("child %d (%s): duration = %v, want ~%v", i, w.name, step.Children[i].Duration, w.dur)
		}
	}
}

func TestBuildKitProgressPhasesBoundsToTopNAndFoldsTheRest(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= core.BuildProgressPhasesTopN+5; i++ {
		b.WriteString("#")
		b.WriteString(strconv.Itoa(i))
		b.WriteString(" [1/1] RUN step")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("\n#")
		b.WriteString(strconv.Itoa(i))
		b.WriteString(" DONE ")
		b.WriteString(strconv.Itoa(i))
		b.WriteString(".0s\n")
	}

	phases := core.BuildKitProgressPhases(b.String())
	if len(phases) != core.BuildProgressPhasesTopN+1 {
		t.Fatalf("expected top-N phases plus one synthetic row, got %d: %+v", len(phases), phases)
	}
	last := phases[len(phases)-1]
	if !strings.Contains(last.Name, "smaller steps") {
		t.Fatalf("expected a trailing synthetic 'smaller steps' row, got: %+v", last)
	}
	if last.Duration != 15*time.Second {
		t.Fatalf("expected the folded row to keep the real total (15s), got %v", last.Duration)
	}
}

func TestBuildKitProgressPhasesIsEmptyForUnrecognisableOutput(t *testing.T) {
	if phases := core.BuildKitProgressPhases("just some chatter\nand more chatter\n"); len(phases) != 0 {
		t.Fatalf("unrecognisable output should yield no phases, got %+v", phases)
	}
	if phases := core.BuildKitProgressPhases(""); len(phases) != 0 {
		t.Fatalf("empty output should yield no phases, got %+v", phases)
	}
}

func TestAttachBuildProgressPhasesAttachesUnderThePlatformStep(t *testing.T) {
	clock := newFakeClock()
	root := core.NewStepTiming("build", clock.now)
	image := root.Child("erun-devops")
	clock.advance(200 * time.Second)
	platform := image.AddFinishedChild("linux/amd64", 200*time.Second, nil, nil, nil)

	output := `#4 [3/3] RUN make check
#4 0.10 >> lint
#4 150.30 >> playwright
#4 DONE 200.00s`
	core.AttachBuildProgressPhases(platform, output)

	if len(platform.Children) != 1 {
		t.Fatalf("expected 1 Dockerfile-step child under the platform step, got %d", len(platform.Children))
	}
	stage := platform.Children[0]
	if stage.Name != "RUN make check" {
		t.Fatalf("expected the RUN step as a child, got %q", stage.Name)
	}
	if len(stage.Children) != 2 {
		t.Fatalf("expected 2 make-phase grandchildren, got %d", len(stage.Children))
	}
	if stage.Children[0].Name != "lint" || stage.Children[1].Name != "playwright" {
		t.Fatalf("unexpected make-phase children: %+v", stage.Children)
	}

	rows := core.RenderStepTimingRows(root, 0)
	joined := strings.Join(rows, "\n")
	for _, want := range []string{"RUN make check", "lint", "playwright"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected the rendered table to name %q, got:\n%s", want, joined)
		}
	}
}

func TestAttachBuildProgressPhasesIsANoOpForUnparseableOutput(t *testing.T) {
	clock := newFakeClock()
	root := core.NewStepTiming("build", clock.now)
	platform := root.AddFinishedChild("linux/amd64", 5*time.Second, nil, nil, nil)

	core.AttachBuildProgressPhases(platform, "docker: not enough disk space\n")
	if len(platform.Children) != 0 {
		t.Fatalf("expected no children attached from unparseable output, got %d", len(platform.Children))
	}
}

// Once targets interleave, marker spans overlap, so summing them misleads.
func TestMarkerPhasesLabelsConcurrentSpansAndDropsSentinel(t *testing.T) {
	markers := []core.BuildKitPhaseMarker{
		{OffsetSecs: 0, Name: "concurrent-phase-spans: check-gate runs 10 targets at -j10"},
		{OffsetSecs: 1, Name: "lint"},
		{OffsetSecs: 5, Name: "test-frontend"},
	}
	phases := core.MarkerPhases(markers, 9)
	if len(phases) != 2 {
		t.Fatalf("expected the sentinel to be consumed, got %d phases: %+v", len(phases), phases)
	}
	for _, p := range phases {
		if !strings.HasSuffix(p.Name, core.BuildProgressConcurrentSuffix) {
			t.Errorf("phase %q is not marked as an elapsed window", p.Name)
		}
	}
}

func TestMarkerPhasesLeavesSerialSpansUnlabelled(t *testing.T) {
	phases := core.MarkerPhases([]core.BuildKitPhaseMarker{{OffsetSecs: 0, Name: "lint"}, {OffsetSecs: 4, Name: "test"}}, 10)
	for _, p := range phases {
		if strings.Contains(p.Name, core.BuildProgressConcurrentSuffix) {
			t.Errorf("serial phase %q must not be marked concurrent", p.Name)
		}
	}
}

// parallel-gate.sh replays output after every job finishes, so marker spans measure the replay.
func TestMarkerPhasesPrefersSelfReportedDuration(t *testing.T) {
	markers := []core.BuildKitPhaseMarker{
		{OffsetSecs: 0, Name: "concurrent-phase-spans: check-gate runs 11 targets at -j11"},
		{OffsetSecs: 10, Name: "golangci-lint erun-ui [83s]"},
		{OffsetSecs: 11, Name: "helm-chart-tests [7s]"},
	}
	phases := core.MarkerPhases(markers, 200)
	if len(phases) != 2 {
		t.Fatalf("expected 2 phases, got %d: %+v", len(phases), phases)
	}
	if phases[0].Name != "golangci-lint erun-ui" || phases[0].Duration != 83*time.Second {
		t.Errorf("phase 0 = %q/%s, want golangci-lint erun-ui/83s", phases[0].Name, phases[0].Duration)
	}
	if phases[1].Name != "helm-chart-tests" || phases[1].Duration != 7*time.Second {
		t.Errorf("phase 1 = %q/%s, want helm-chart-tests/7s", phases[1].Name, phases[1].Duration)
	}
}
