package eruncommon

import (
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

// buildHeartbeatInterval is how long a build run may stay silent before it says
// what it is still doing.
//
// The silence it bounds is deliberate everywhere else: below debug verbosity a
// real `docker build` has its BuildKit output captured rather than streamed (see
// runDockerBuildOnce), so the only lines a run emits are the decision traces
// that precede each image. That is fine while images promote from a fingerprint
// cache in milliseconds, but one Dockerfile in every project is never promoted
// — the one whose test stage runs the project's own gate, which must run for
// real — and it is minutes of no output at all. A working build and a wedged
// one then look exactly alike, and the reader has nothing to measure the
// difference with.
//
// Thirty seconds keeps this a heartbeat rather than a stream: two lines a
// minute, each one moving, is enough to tell a build is alive without printing
// anything a reader has to read past.
const buildHeartbeatInterval = 30 * time.Second

// buildHeartbeat is one build run's liveness signal. Every image build registers
// itself on the way in and clears itself on the way out, and one ticker emits a
// single line naming everything still building.
//
// One emitter per run rather than one per image, for two reasons. A concurrent
// wave can have several images in flight, and a heartbeat per image would make
// the number of lines a function of the degree of parallelism — the opposite of
// low-noise. And one emitter means one goroutine emitting while the run's own
// traces are emitted by whichever goroutine is driving it, so the only two
// writers of the log stream can be serialized against each other (see
// serializedWriter).
type buildHeartbeat struct {
	now  func() time.Time
	emit func(string)
	// ticks is the heartbeat's clock. It is a channel rather than an internal
	// time.Ticker so a test can drive the loop tick by tick instead of waiting
	// on wall-clock time; newBuildHeartbeat hands it a real ticker's channel and
	// keeps the ticker so a finish can stop it.
	ticks      <-chan time.Time
	stopTicker func()

	mu     sync.Mutex
	active []*buildHeartbeatBuild

	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// buildHeartbeatBuild is one image registered as building, with the instant it
// started — the elapsed time in the heartbeat line is measured from here.
type buildHeartbeatBuild struct {
	label   string
	started time.Time
}

func newBuildHeartbeat(interval time.Duration, now func() time.Time, emit func(string)) *buildHeartbeat {
	ticker := time.NewTicker(interval)
	return &buildHeartbeat{now: now, emit: emit, ticks: ticker.C, stopTicker: ticker.Stop}
}

// start runs the heartbeat until finish is called.
func (h *buildHeartbeat) start() {
	if h == nil {
		return
	}
	h.stop = make(chan struct{})
	h.done = make(chan struct{})
	go func() {
		defer close(h.done)
		for {
			select {
			case <-h.stop:
				return
			case _, ok := <-h.ticks:
				if !ok {
					return
				}
				h.beat()
			}
		}
	}()
}

// finish stops the heartbeat and waits for its loop to exit, so no line is ever
// emitted for a run that has already ended — the same rule the job supervisor's
// record writer follows for a late progress tick.
func (h *buildHeartbeat) finish() {
	if h == nil {
		return
	}
	h.stopOnce.Do(func() {
		if h.stopTicker != nil {
			h.stopTicker()
		}
		if h.stop == nil {
			return
		}
		close(h.stop)
		<-h.done
	})
}

// begin registers one image as building and returns the function that clears it.
// The returned function is idempotent, so a caller can defer it and also call it
// early without removing a second, unrelated build.
func (h *buildHeartbeat) begin(label string) func() {
	if h == nil || strings.TrimSpace(label) == "" {
		return func() {}
	}
	tracked := &buildHeartbeatBuild{label: label, started: h.now()}
	h.mu.Lock()
	h.active = append(h.active, tracked)
	h.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			for i, build := range h.active {
				if build == tracked {
					h.active = append(h.active[:i], h.active[i+1:]...)
					return
				}
			}
		})
	}
}

// beat emits one line for everything still building, or nothing when nothing is.
// It is the emission seam: the ticker loop calls it, and so does a test that
// wants to observe the line without waiting on a clock.
func (h *buildHeartbeat) beat() {
	if h == nil {
		return
	}
	line := h.line()
	if line == "" {
		return
	}
	h.emit(line)
}

// line renders what is still building right now, in registration order — the
// order the images were started in, so a line is reproducible given the same
// schedule. Empty when nothing is in flight.
func (h *buildHeartbeat) line() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.active) == 0 {
		return ""
	}
	now := h.now()
	snapshots := make([]buildHeartbeatSnapshot, 0, len(h.active))
	for _, build := range h.active {
		snapshots = append(snapshots, buildHeartbeatSnapshot{Label: build.label, Elapsed: now.Sub(build.started)})
	}
	return formatBuildHeartbeat(snapshots)
}

// buildHeartbeatSnapshot is one still-building image at the instant a heartbeat
// is rendered.
type buildHeartbeatSnapshot struct {
	Label   string
	Elapsed time.Duration
}

// formatBuildHeartbeat renders the heartbeat line. An image's own elapsed time
// stays next to its name however many are in flight: the elapsed time is the
// part that proves the run is moving, and a bare count of images would not say
// which of them is the one taking the time.
func formatBuildHeartbeat(snapshots []buildHeartbeatSnapshot) string {
	if len(snapshots) == 0 {
		return ""
	}
	parts := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		parts = append(parts, snapshot.Label+", "+formatBuildHeartbeatElapsed(snapshot.Elapsed)+" elapsed")
	}
	if len(parts) == 1 {
		return "still building " + parts[0]
	}
	return "still building " + strconv.Itoa(len(parts)) + " images: " + strings.Join(parts, "; ")
}

// formatBuildHeartbeatElapsed rounds to the second, which is the resolution
// every other elapsed time a build reports is rendered at.
func formatBuildHeartbeatElapsed(elapsed time.Duration) string {
	if elapsed < 0 {
		elapsed = 0
	}
	return elapsed.Round(time.Second).String()
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
func withBuildProgress(ctx Context) (Context, func()) {
	if ctx.progress != nil || ctx.DryRun || ctx.Verbosity >= VerbosityDebug {
		return ctx, func() {}
	}
	// One mutex-guarded stream: the heartbeat goroutine and the run's own trace
	// lines are the only writers of it while a heartbeat is active, and they are
	// on different goroutines by construction.
	logger := ctx.Logger.withStdout(&serializedWriter{w: ctx.Logger.stdoutWriter()})
	heartbeat := newBuildHeartbeat(buildHeartbeatInterval, time.Now, logger.Info)
	heartbeat.start()
	ctx.Logger = logger
	ctx.progress = heartbeat
	return ctx, heartbeat.finish
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
