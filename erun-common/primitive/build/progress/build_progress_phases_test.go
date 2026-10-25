package progress

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

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
