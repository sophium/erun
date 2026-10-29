package progress

import (
	"io"
	"sync"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

// buildHeartbeatInterval bounds how long a build may stay silent: below debug verbosity
// BuildKit output is captured, so a long build and a wedged one otherwise look alike.
const buildHeartbeatInterval = 30 * time.Second

func newBuildHeartbeat(interval time.Duration, now func() time.Time, emit func(string)) *core.BuildHeartbeat {
	ticker := time.NewTicker(interval)
	return &core.BuildHeartbeat{Now: now, Emit: emit, Ticks: ticker.C, StopTicker: ticker.Stop}
}

// WithBuildProgress reuses an emitter already on ctx so the clock is not restarted per
// image. Skipped in dry-run (stable goldens) and at debug verbosity (output streams live).
func WithBuildProgress(ctx core.Context) (core.Context, func()) {
	if ctx.Progress != nil || ctx.DryRun || ctx.Verbosity >= core.VerbosityDebug {
		return ctx, func() {}
	}
	logger := ctx.Logger.WithStdout(&serializedWriter{w: ctx.Logger.StdoutWriter()})
	heartbeat := newBuildHeartbeat(buildHeartbeatInterval, time.Now, logger.Info)
	heartbeat.Start()
	ctx.Logger = logger
	ctx.Progress = heartbeat
	return ctx, heartbeat.Finish
}

// serializedWriter shares one stream between the heartbeat and the run's goroutine.
type serializedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *serializedWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
