package core

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// BuildHeartbeat emits one line per tick naming every image still building. One
// emitter per run keeps noise independent of parallelism and lets the log stream
// writers be serialized (see serializedWriter).
type BuildHeartbeat struct {
	Now  func() time.Time
	Emit func(string)
	// Ticks is a channel so tests can drive the loop without wall-clock time.
	Ticks      <-chan time.Time
	StopTicker func()

	mu     sync.Mutex
	active []*buildHeartbeatBuild

	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

type buildHeartbeatBuild struct {
	label   string
	started time.Time
}

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

// Finish stops the heartbeat and waits for its loop, so no line follows the run's end.
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

// Begin registers one image as building and returns an idempotent clear function.
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

// Beat emits one line for everything still building, or nothing.
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

// Line renders in-flight images in registration order; empty when none.
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

type BuildHeartbeatSnapshot struct {
	Label   string
	Elapsed time.Duration
}

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

func formatBuildHeartbeatElapsed(elapsed time.Duration) string {
	if elapsed < 0 {
		elapsed = 0
	}
	return elapsed.Round(time.Second).String()
}
