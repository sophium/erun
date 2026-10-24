package deploy

import (
	"time"

	"github.com/sophium/erun/erun-common/base/config"
)

// RuntimeUsageHistory retains what a single read cannot know. Its aggregates
// are the point of the type: memory.peak resets when the container restarts and
// the rolling window drops its oldest sample, so neither the kernel counter nor
// the window can be the memory of record for an environment's high-water mark.
// The aggregates below are monotonic and are never rolled off; Samples is the
// bounded window kept for recency.
type RuntimeUsageHistory struct {
	// FirstObservedAt anchors the evidence window. A recommendation that cannot
	// say how long it watched is not evidence, and the shrink direction is
	// gated on this span.
	FirstObservedAt time.Time `json:"firstObservedAt,omitzero"`
	LastObservedAt  time.Time `json:"lastObservedAt,omitzero"`

	// ObservedPeakMemoryBytes is the highest memory reading across every
	// container lifetime observed, which is the figure a shrink must keep clear
	// of. An environment whose real peak happened before the last restart would
	// otherwise be sized from a counter that no longer remembers it.
	ObservedPeakMemoryBytes int64 `json:"observedPeakMemoryBytes,omitempty"`
	// ObservedOOMKills accumulates the kernel's own kill count across restarts.
	// memory.events resets with the container, so a total needs the deltas.
	ObservedOOMKills int64 `json:"observedOomKills,omitempty"`

	// ObservedPeakCPUMilli is the highest per-interval CPU rate seen, derived
	// from consecutive readings' cumulative cpu.stat usage_usec over the wall
	// time between them. It is not a loadavg figure and must not be compared
	// with one: a loadavg counts the host's runnable queue, so a busy neighbour
	// raises it while this container's own consumption is unchanged.
	//
	// It is an average over the interval between two ticks, so a burst shorter
	// than that interval is flattened into it — which is why the shrink
	// direction keeps a multiple of this figure rather than treating it as the
	// true peak, and why any throttling at all disqualifies a shrink.
	ObservedPeakCPUMilli int64 `json:"observedPeakCpuMilli,omitempty"`
	// ObservedPeriods / ObservedThrottledPeriods accumulate cpu.stat's
	// scheduling-period counters across restarts, so the throttle ratio spans
	// the whole observation rather than the current container lifetime.
	ObservedPeriods          int64 `json:"observedPeriods,omitempty"`
	ObservedThrottledPeriods int64 `json:"observedThrottledPeriods,omitempty"`

	// Restarts counts the container restarts inferred from a counter going
	// backwards. It is reported alongside a recommendation because a history
	// spanning restarts is stronger evidence than one that does not, and a
	// reader needs to know the peak survived them.
	Restarts int `json:"restarts,omitempty"`

	// Samples is a rolling window of the raw readings RunRuntimeUsage /
	// ReadLocalRuntimeUsage produced, kept for recency rather than for the
	// aggregates above (which are derived once, at append time, and never
	// recomputed from this window).
	Samples []config.RuntimeUsage `json:"samples,omitempty"`
}

// Latest returns the newest retained sample.
func (h RuntimeUsageHistory) Latest() (config.RuntimeUsage, bool) {
	if len(h.Samples) == 0 {
		return config.RuntimeUsage{}, false
	}
	return h.Samples[len(h.Samples)-1], true
}

// ObservedWindow is how long this environment has been watched.
func (h RuntimeUsageHistory) ObservedWindow() time.Duration {
	if h.FirstObservedAt.IsZero() || h.LastObservedAt.IsZero() {
		return 0
	}
	window := h.LastObservedAt.Sub(h.FirstObservedAt)
	if window < 0 {
		return 0
	}
	return window
}
