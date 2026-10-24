package core

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// BuildHeartbeat is one build run's liveness signal. Every image build registers
// itself on the way in and clears itself on the way out, and one ticker emits a
// single line naming everything still building.
//
// One emitter per run rather than one per image, for two reasons. A concurrent
// run can have several images in flight, and a heartbeat per image would make
// the number of lines a function of the degree of parallelism — the opposite of
// low-noise. And one emitter means one goroutine emitting while the run's own
// traces are emitted by whichever goroutine is driving it, so the only two
// writers of the log stream can be serialized against each other (see
// serializedWriter).
type BuildHeartbeat struct {
	Now  func() time.Time
	Emit func(string)
	// Ticks is the heartbeat's clock. It is a channel rather than an internal
	// time.Ticker so a test can drive the loop tick by tick instead of waiting
	// on wall-clock time; newBuildHeartbeat hands it a real ticker's channel and
	// keeps the ticker so a finish can stop it.
	Ticks      <-chan time.Time
	StopTicker func()

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

// Start runs the heartbeat until finish is called.
func (h *BuildHeartbeat) Start() {
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
			case _, ok := <-h.Ticks:
				if !ok {
					return
				}
				h.Beat()
			}
		}
	}()
}

// Finish stops the heartbeat and waits for its loop to exit, so no line is ever
// emitted for a run that has already ended — the same rule the job supervisor's
// record writer follows for a late progress tick.
func (h *BuildHeartbeat) Finish() {
	if h == nil {
		return
	}
	h.stopOnce.Do(func() {
		if h.StopTicker != nil {
			h.StopTicker()
		}
		if h.stop == nil {
			return
		}
		close(h.stop)
		<-h.done
	})
}

// Begin registers one image as building and returns the function that clears it.
// The returned function is idempotent, so a caller can defer it and also call it
// early without removing a second, unrelated build.
func (h *BuildHeartbeat) Begin(label string) func() {
	if h == nil || strings.TrimSpace(label) == "" {
		return func() {}
	}
	tracked := &buildHeartbeatBuild{label: label, started: h.Now()}
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

// Beat emits one line for everything still building, or nothing when nothing is.
// It is the emission seam: the ticker loop calls it, and so does a test that
// wants to observe the line without waiting on a clock.
func (h *BuildHeartbeat) Beat() {
	if h == nil {
		return
	}
	line := h.Line()
	if line == "" {
		return
	}
	h.Emit(line)
}

// Line renders what is still building right now, in registration order — the
// order the images were started in, so a line is reproducible given the same
// schedule. Empty when nothing is in flight.
func (h *BuildHeartbeat) Line() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.active) == 0 {
		return ""
	}
	now := h.Now()
	snapshots := make([]BuildHeartbeatSnapshot, 0, len(h.active))
	for _, build := range h.active {
		snapshots = append(snapshots, BuildHeartbeatSnapshot{Label: build.label, Elapsed: now.Sub(build.started)})
	}
	return FormatBuildHeartbeat(snapshots)
}

// BuildHeartbeatSnapshot is one still-building image at the instant a heartbeat
// is rendered.
type BuildHeartbeatSnapshot struct {
	Label   string
	Elapsed time.Duration
}

// FormatBuildHeartbeat renders the heartbeat line. An image's own elapsed time
// stays next to its name however many are in flight: the elapsed time is the
// part that proves the run is moving, and a bare count of images would not say
// which of them is the one taking the time.
func FormatBuildHeartbeat(snapshots []BuildHeartbeatSnapshot) string {
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
