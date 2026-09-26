package eruncommon

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

// The line has to name the image and how long it has been going, because those
// are the two facts a reader watching a silent log has no other way to get: the
// elapsed time is what distinguishes a build that is working from one that is
// wedged.
func TestFormatBuildHeartbeatNamesTheImageAndItsElapsedTime(t *testing.T) {
	got := formatBuildHeartbeat([]buildHeartbeatSnapshot{{Label: "erun-devops", Elapsed: 2*time.Minute + 30*time.Second}})
	if got != "still building erun-devops, 2m30s elapsed" {
		t.Fatalf("heartbeat line = %q", got)
	}
}

// A concurrent build can have several images in flight, and the heartbeat must
// still say which one is the long one rather than only how many there are.
func TestFormatBuildHeartbeatNamesEveryImageStillBuilding(t *testing.T) {
	got := formatBuildHeartbeat([]buildHeartbeatSnapshot{
		{Label: "erun-devops", Elapsed: 9 * time.Minute},
		{Label: "erun-console", Elapsed: 70 * time.Second},
	})
	want := "still building 2 images: erun-devops, 9m0s elapsed; erun-console, 1m10s elapsed"
	if got != want {
		t.Fatalf("heartbeat line = %q, want %q", got, want)
	}
	if empty := formatBuildHeartbeat(nil); empty != "" {
		t.Fatalf("nothing in flight must render nothing, got %q", empty)
	}
}

// The emission seam itself: a registered build is named on every beat, and a
// build that has finished is not. This is the state the report described — for
// the whole of a long rebuild the log said nothing about what was building.
func TestBuildHeartbeatBeatsOnlyWhileABuildIsStillRunning(t *testing.T) {
	clock := time.Unix(0, 0)
	heartbeat := &buildHeartbeat{now: func() time.Time { return clock }, emit: func(string) {}, ticks: make(chan time.Time)}

	if line := heartbeat.line(); line != "" {
		t.Fatalf("an idle heartbeat must say nothing, got %q", line)
	}

	done := heartbeat.begin("erun-devops")
	clock = clock.Add(4 * time.Minute)
	if line := heartbeat.line(); line != "still building erun-devops, 4m0s elapsed" {
		t.Fatalf("a running build must be named, got %q", line)
	}

	done()
	clock = clock.Add(time.Minute)
	if line := heartbeat.line(); line != "" {
		t.Fatalf("a finished build must be cleared, got %q", line)
	}
	done() // idempotent: a second clear must not drop another image's registration
}

// Two images building at once are tracked independently, so the one that finishes
// first stops being named while the other keeps its own elapsed time.
func TestBuildHeartbeatTracksConcurrentBuildsIndependently(t *testing.T) {
	clock := time.Unix(0, 0)
	heartbeat := &buildHeartbeat{now: func() time.Time { return clock }, emit: func(string) {}, ticks: make(chan time.Time)}

	first := heartbeat.begin("erun-devops")
	clock = clock.Add(30 * time.Second)
	second := heartbeat.begin("erun-console")
	clock = clock.Add(90 * time.Second)

	want := "still building 2 images: erun-devops, 2m0s elapsed; erun-console, 1m30s elapsed"
	if line := heartbeat.line(); line != want {
		t.Fatalf("heartbeat line = %q, want %q", line, want)
	}

	first()
	if line := heartbeat.line(); line != "still building erun-console, 1m30s elapsed" {
		t.Fatalf("only the still-running build must remain, got %q", line)
	}
	second()
	if line := heartbeat.line(); line != "" {
		t.Fatalf("nothing must remain, got %q", line)
	}
}

// The loop emits on its own ticks, and finish waits for it to be gone: a
// heartbeat that outlived its run would report a build that already finished.
// Ticks are driven by hand so the test does not wait on wall-clock time.
func TestBuildHeartbeatEmitsOnEveryTickUntilItIsFinished(t *testing.T) {
	ticks := make(chan time.Time)
	emitted := make(chan string)
	clock := time.Unix(0, 0)
	heartbeat := &buildHeartbeat{
		now:   func() time.Time { return clock },
		emit:  func(line string) { emitted <- line },
		ticks: ticks,
	}
	heartbeat.start()
	done := heartbeat.begin("erun-devops")

	clock = clock.Add(30 * time.Second)
	ticks <- time.Now()
	if line := <-emitted; !strings.Contains(line, "still building erun-devops, 30s elapsed") {
		t.Fatalf("expected a heartbeat naming the running build, got %q", line)
	}

	heartbeat.finish()
	// The loop is gone, so nothing is left to receive a tick: sends used to be
	// accepted by it, and now nothing but this goroutine can.
	select {
	case ticks <- time.Now():
		t.Fatal("the heartbeat loop outlived finish and is still ticking")
	default:
	}
	done()
}

// Dry-run does no work and must keep the goldens stable; at debug verbosity the
// build's own BuildKit output streams live, so a heartbeat would be a second,
// redundant signal. Neither installs one.
func TestWithBuildProgressInstallsOneHeartbeatPerRun(t *testing.T) {
	if ctx, _ := withBuildProgress(Context{DryRun: true}); ctx.progress != nil {
		t.Fatal("dry-run must not install a heartbeat")
	}
	if ctx, _ := withBuildProgress(Context{Verbosity: VerbosityDebug}); ctx.progress != nil {
		t.Fatal("a run whose build output streams live must not install a heartbeat")
	}

	var log bytes.Buffer
	ctx, stop := withBuildProgress(Context{Logger: NewLoggerWithWriters(VerbosityInfo, &log, io.Discard)})
	defer stop()
	if ctx.progress == nil {
		t.Fatal("a real build run must install a heartbeat")
	}
	// The installed heartbeat writes to the run's own log stream, which is where
	// the reader watching for the build's next line is looking.
	cleared := ctx.progress.begin("erun-devops")
	ctx.progress.beat()
	cleared()
	if got := log.String(); !strings.Contains(got, "still building erun-devops, 0s elapsed") {
		t.Fatalf("heartbeat must reach the run's log stream, got %q", got)
	}
	// The sequential path calls this once per image; a second emitter would
	// restart the clock every image's elapsed time is measured from.
	again, secondStop := withBuildProgress(ctx)
	defer secondStop()
	if again.progress != ctx.progress {
		t.Fatal("a run must reuse the heartbeat it already has")
	}
}

// The reproduction of the reported failure: through the whole of a long image
// build, nothing in the log named the image that was building or how long it had
// been going, so a working rebuild was indistinguishable from a wedged one.
func TestExecuteDockerBuildNamesTheImageItIsBuildingWhileItRuns(t *testing.T) {
	clock := time.Unix(0, 0)
	heartbeat := &buildHeartbeat{now: func() time.Time { return clock }, emit: func(string) {}, ticks: make(chan time.Time)}
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, io.Discard, io.Discard), progress: heartbeat}

	spec := DockerBuildSpec{
		Image:          DockerImageReference{Tag: "ghcr.io/sophium/erun-devops:1.0.290", ImageName: "erun-devops"},
		DockerfilePath: "Dockerfile", ContextDir: ".",
	}
	duringBuild := ""
	build := func(DockerBuildSpec, io.Writer, io.Writer) error {
		clock = clock.Add(11 * time.Minute)
		duringBuild = heartbeat.line()
		return nil
	}

	if err := executeDockerBuild(ctx, spec, build, io.Discard, io.Discard); err != nil {
		t.Fatalf("executeDockerBuild: %v", err)
	}
	if duringBuild != "still building erun-devops, 11m0s elapsed" {
		t.Fatalf("mid-build heartbeat = %q, want the image and its elapsed time", duringBuild)
	}
	if after := heartbeat.line(); after != "" {
		t.Fatalf("the build must stop being named once it finishes, got %q", after)
	}
}
