package run

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/primitive/build/progress"
)

func TestFormatBuildHeartbeatNamesTheImageAndItsElapsedTime(t *testing.T) {
	got := core.FormatBuildHeartbeat([]core.BuildHeartbeatSnapshot{{Label: "erun-devops", Elapsed: 2*time.Minute + 30*time.Second}})
	if got != "still building erun-devops, 2m30s elapsed" {
		t.Fatalf("heartbeat line = %q", got)
	}
}

func TestFormatBuildHeartbeatNamesEveryImageStillBuilding(t *testing.T) {
	got := core.FormatBuildHeartbeat([]core.BuildHeartbeatSnapshot{
		{Label: "erun-devops", Elapsed: 9 * time.Minute},
		{Label: "erun-console", Elapsed: 70 * time.Second},
	})
	want := "still building 2 images: erun-devops, 9m0s elapsed; erun-console, 1m10s elapsed"
	if got != want {
		t.Fatalf("heartbeat line = %q, want %q", got, want)
	}
	if empty := core.FormatBuildHeartbeat(nil); empty != "" {
		t.Fatalf("nothing in flight must render nothing, got %q", empty)
	}
}

func TestBuildHeartbeatBeatsOnlyWhileABuildIsStillRunning(t *testing.T) {
	clock := time.Unix(0, 0)
	heartbeat := &core.BuildHeartbeat{Now: func() time.Time { return clock }, Emit: func(string) {}, Ticks: make(chan time.Time)}

	if line := heartbeat.Line(); line != "" {
		t.Fatalf("an idle heartbeat must say nothing, got %q", line)
	}

	done := heartbeat.Begin("erun-devops")
	clock = clock.Add(4 * time.Minute)
	if line := heartbeat.Line(); line != "still building erun-devops, 4m0s elapsed" {
		t.Fatalf("a running build must be named, got %q", line)
	}

	done()
	clock = clock.Add(time.Minute)
	if line := heartbeat.Line(); line != "" {
		t.Fatalf("a finished build must be cleared, got %q", line)
	}
	done() // idempotent: a second clear must not drop another image's registration
}

func TestBuildHeartbeatTracksConcurrentBuildsIndependently(t *testing.T) {
	clock := time.Unix(0, 0)
	heartbeat := &core.BuildHeartbeat{Now: func() time.Time { return clock }, Emit: func(string) {}, Ticks: make(chan time.Time)}

	first := heartbeat.Begin("erun-devops")
	clock = clock.Add(30 * time.Second)
	second := heartbeat.Begin("erun-console")
	clock = clock.Add(90 * time.Second)

	want := "still building 2 images: erun-devops, 2m0s elapsed; erun-console, 1m30s elapsed"
	if line := heartbeat.Line(); line != want {
		t.Fatalf("heartbeat line = %q, want %q", line, want)
	}

	first()
	if line := heartbeat.Line(); line != "still building erun-console, 1m30s elapsed" {
		t.Fatalf("only the still-running build must remain, got %q", line)
	}
	second()
	if line := heartbeat.Line(); line != "" {
		t.Fatalf("nothing must remain, got %q", line)
	}
}

// Ticks are driven by hand so the test does not wait on wall-clock time.
func TestBuildHeartbeatEmitsOnEveryTickUntilItIsFinished(t *testing.T) {
	ticks := make(chan time.Time)
	emitted := make(chan string)
	clock := time.Unix(0, 0)
	heartbeat := &core.BuildHeartbeat{
		Now:   func() time.Time { return clock },
		Emit:  func(line string) { emitted <- line },
		Ticks: ticks,
	}
	heartbeat.Start()
	done := heartbeat.Begin("erun-devops")

	clock = clock.Add(30 * time.Second)
	ticks <- time.Now()
	if line := <-emitted; !strings.Contains(line, "still building erun-devops, 30s elapsed") {
		t.Fatalf("expected a heartbeat naming the running build, got %q", line)
	}

	heartbeat.Finish()
	select {
	case ticks <- time.Now():
		t.Fatal("the heartbeat loop outlived finish and is still ticking")
	default:
	}
	done()
}

func TestWithBuildProgressInstallsOneHeartbeatPerRun(t *testing.T) {
	if ctx, _ := progress.WithBuildProgress(core.Context{DryRun: true}); ctx.Progress != nil {
		t.Fatal("dry-run must not install a heartbeat")
	}
	if ctx, _ := progress.WithBuildProgress(core.Context{Verbosity: core.VerbosityDebug}); ctx.Progress != nil {
		t.Fatal("a run whose build output streams live must not install a heartbeat")
	}

	var log bytes.Buffer
	ctx, stop := progress.WithBuildProgress(core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, io.Discard)})
	defer stop()
	if ctx.Progress == nil {
		t.Fatal("a real build run must install a heartbeat")
	}
	cleared := ctx.Progress.Begin("erun-devops")
	ctx.Progress.Beat()
	cleared()
	if got := log.String(); !strings.Contains(got, "still building erun-devops, 0s elapsed") {
		t.Fatalf("heartbeat must reach the run's log stream, got %q", got)
	}
	again, secondStop := progress.WithBuildProgress(ctx)
	defer secondStop()
	if again.Progress != ctx.Progress {
		t.Fatal("a run must reuse the heartbeat it already has")
	}
}

func TestExecuteDockerBuildNamesTheImageItIsBuildingWhileItRuns(t *testing.T) {
	clock := time.Unix(0, 0)
	heartbeat := &core.BuildHeartbeat{Now: func() time.Time { return clock }, Emit: func(string) {}, Ticks: make(chan time.Time)}
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, io.Discard, io.Discard), Progress: heartbeat}

	spec := seam.DockerBuildSpec{
		Image:          model.DockerImageReference{Tag: "ghcr.io/sophium/erun-devops:1.0.290", ImageName: "erun-devops"},
		DockerfilePath: "Dockerfile", ContextDir: ".",
	}
	duringBuild := ""
	build := func(seam.DockerBuildSpec, io.Writer, io.Writer) error {
		clock = clock.Add(11 * time.Minute)
		duringBuild = heartbeat.Line()
		return nil
	}

	if err := executeDockerBuild(ctx, spec, build, io.Discard, io.Discard); err != nil {
		t.Fatalf("executeDockerBuild: %v", err)
	}
	if duringBuild != "still building erun-devops, 11m0s elapsed" {
		t.Fatalf("mid-build heartbeat = %q, want the image and its elapsed time", duringBuild)
	}
	if after := heartbeat.Line(); after != "" {
		t.Fatalf("the build must stop being named once it finishes, got %q", after)
	}
}
