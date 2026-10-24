package eruncommon

import (
	"io"
	"sync"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

// buildHeartbeatInterval is how long a build run may stay silent before it says
// what it is still doing.
//
// The silence it bounds is deliberate everywhere else: below debug verbosity a
// real `docker build` has its BuildKit output captured rather than streamed (see
// runDockerBuildOnce), so the only lines a run emits are the decision traces
// that precede each image. That is fine while images promote from a fingerprint
// cache in milliseconds, but a component whose inputs changed rebuilds, and one
// whose Dockerfile runs a long test stage is minutes of no output at all. A working build and a wedged
// one then look exactly alike, and the reader has nothing to measure the
// difference with.
//
// Thirty seconds keeps this a heartbeat rather than a stream: two lines a
// minute, each one moving, is enough to tell a build is alive without printing
// anything a reader has to read past.
const buildHeartbeatInterval = 30 * time.Second

func newBuildHeartbeat(interval time.Duration, now func() time.Time, emit func(string)) *core.BuildHeartbeat {
	ticker := time.NewTicker(interval)
	return &core.BuildHeartbeat{Now: now, Emit: emit, Ticks: ticker.C, StopTicker: ticker.Stop}
}

// withBuildProgress returns ctx carrying this run's heartbeat, creating one when
// it does not have one yet, plus the function that stops it. An emitter already
// on the context is reused untouched: the sequential path calls RunDockerBuild
// once per image, and replacing the run's emitter per image would restart the
// clock the line is measured from.
//
// Nothing is installed in dry-run, which does no work and must keep the goldens
// stable, or at debug verbosity and above, where the build's own BuildKit
// output streams live and the heartbeat would be a second, redundant signal.
func withBuildProgress(ctx core.Context) (core.Context, func()) {
	if ctx.Progress != nil || ctx.DryRun || ctx.Verbosity >= core.VerbosityDebug {
		return ctx, func() {}
	}
	// One mutex-guarded stream: the heartbeat goroutine and the run's own trace
	// lines are the only writers of it while a heartbeat is active, and they are
	// on different goroutines by construction.
	logger := ctx.Logger.WithStdout(&serializedWriter{w: ctx.Logger.StdoutWriter()})
	heartbeat := newBuildHeartbeat(buildHeartbeatInterval, time.Now, logger.Info)
	heartbeat.Start()
	ctx.Logger = logger
	ctx.Progress = heartbeat
	return ctx, heartbeat.Finish
}

// serializedWriter makes one stream safe to share between the heartbeat
// goroutine and the goroutine driving the run. It is not a general-purpose
// writer: it exists for the one stream those two share, for the duration of one
// build run.
type serializedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *serializedWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
