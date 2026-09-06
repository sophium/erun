package eruncommon

import (
	"strconv"
	"strings"
	"testing"
	"time"
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

	phases := buildKitProgressPhases(output)
	if len(phases) != 3 {
		t.Fatalf("expected 3 phases (internal, COPY, RUN — the CACHED FROM has no DONE), got %d: %+v", len(phases), phases)
	}

	byName := map[string]buildProgressPhase{}
	for _, p := range phases {
		byName[p.name] = p
	}
	if _, ok := byName["FROM docker.io/library/golang:1.26.0"]; ok {
		t.Fatalf("a cached step with no DONE line must not be reported as a zero-duration phase, got: %+v", phases)
	}
	if got := byName["load build definition from Dockerfile"].duration; got != 0 {
		t.Fatalf("expected 0s for the internal step, got %v", got)
	}
	if got := byName["COPY . ."].duration; got != 1500*time.Millisecond {
		t.Fatalf("expected 1.5s for the COPY step, got %v", got)
	}
	if got := byName["RUN make check"].duration; got != 200250*time.Millisecond {
		t.Fatalf("expected 200.25s for the RUN step, got %v", got)
	}
}

func TestBuildKitProgressPhasesNestsMakefilePhaseMarkersUnderTheirStep(t *testing.T) {
	// Real shape from a gate build: BuildKit prefixes every line a step prints
	// with elapsed seconds since the step started, and the Makefile's own `>>
	// <phase>` markers ride inside that stream.
	output := `#8 [4/8] RUN make check
#8 0.12 >> golangci-lint erun-common
#8 5.20 >> golangci-lint erun-cli
#8 54.73 >> go test erun-ui
#8 90.20 >> go test erun-backend-api
#8 DONE 107.50s`

	phases := buildKitProgressPhases(output)
	if len(phases) != 1 {
		t.Fatalf("expected the one RUN step, got %d: %+v", len(phases), phases)
	}
	step := phases[0]
	if step.name != "RUN make check" || step.duration != 107500*time.Millisecond {
		t.Fatalf("unexpected top-level step: %+v", step)
	}
	if len(step.children) != 4 {
		t.Fatalf("expected 4 make-phase children, got %d: %+v", len(step.children), step.children)
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
		if step.children[i].name != w.name {
			t.Fatalf("child %d: name = %q, want %q (children: %+v)", i, step.children[i].name, w.name, step.children)
		}
		// Floating-point subtraction of the offsets can land a few
		// nanoseconds off an exact decimal value; a millisecond of slack is
		// well below anything that would matter to a reader of the table.
		diff := step.children[i].duration - w.dur
		if diff < -time.Millisecond || diff > time.Millisecond {
			t.Fatalf("child %d (%s): duration = %v, want ~%v", i, w.name, step.children[i].duration, w.dur)
		}
	}
}

func TestBuildKitProgressPhasesBoundsToTopNAndFoldsTheRest(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= buildProgressPhasesTopN+5; i++ {
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

	phases := buildKitProgressPhases(b.String())
	if len(phases) != buildProgressPhasesTopN+1 {
		t.Fatalf("expected top-N phases plus one synthetic row, got %d: %+v", len(phases), phases)
	}
	last := phases[len(phases)-1]
	if !strings.Contains(last.name, "smaller steps") {
		t.Fatalf("expected a trailing synthetic 'smaller steps' row, got: %+v", last)
	}
	// The 5 smallest (durations 1s..5s) fold into the synthetic row: 1+2+3+4+5=15s.
	if last.duration != 15*time.Second {
		t.Fatalf("expected the folded row to keep the real total (15s), got %v", last.duration)
	}
}

func TestBuildKitProgressPhasesIsEmptyForUnrecognisableOutput(t *testing.T) {
	if phases := buildKitProgressPhases("just some chatter\nand more chatter\n"); len(phases) != 0 {
		t.Fatalf("unrecognisable output should yield no phases, got %+v", phases)
	}
	if phases := buildKitProgressPhases(""); len(phases) != 0 {
		t.Fatalf("empty output should yield no phases, got %+v", phases)
	}
}

func TestAttachBuildProgressPhasesAttachesUnderThePlatformStep(t *testing.T) {
	clock := newFakeClock()
	root := newStepTiming("build", clock.now)
	image := root.child("erun-devops")
	clock.advance(200 * time.Second)
	platform := image.addFinishedChild("linux/amd64", 200*time.Second, nil, nil, nil)

	output := `#4 [3/3] RUN make check
#4 0.10 >> lint
#4 150.30 >> playwright
#4 DONE 200.00s`
	attachBuildProgressPhases(platform, output)

	if len(platform.children) != 1 {
		t.Fatalf("expected 1 Dockerfile-step child under the platform step, got %d", len(platform.children))
	}
	stage := platform.children[0]
	if stage.name != "RUN make check" {
		t.Fatalf("expected the RUN step as a child, got %q", stage.name)
	}
	if len(stage.children) != 2 {
		t.Fatalf("expected 2 make-phase grandchildren, got %d", len(stage.children))
	}
	if stage.children[0].name != "lint" || stage.children[1].name != "playwright" {
		t.Fatalf("unexpected make-phase children: %+v", stage.children)
	}

	rows := renderStepTimingRows(root, 0)
	joined := strings.Join(rows, "\n")
	for _, want := range []string{"RUN make check", "lint", "playwright"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected the rendered table to name %q, got:\n%s", want, joined)
		}
	}
}

func TestAttachBuildProgressPhasesIsANoOpForUnparseableOutput(t *testing.T) {
	clock := newFakeClock()
	root := newStepTiming("build", clock.now)
	platform := root.addFinishedChild("linux/amd64", 5*time.Second, nil, nil, nil)

	attachBuildProgressPhases(platform, "docker: not enough disk space\n")
	if len(platform.children) != 0 {
		t.Fatalf("expected no children attached from unparseable output, got %d", len(platform.children))
	}
}
