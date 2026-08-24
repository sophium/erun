package eruncommon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const (
	runtimeUsageHistoryFileName = "usage-history.json"

	// runtimeUsageHistorySampleCap bounds the rolling window of raw samples.
	// The monitor ticks every 30s, so this is roughly two hours of readings —
	// enough to see recent rates, and a fixed cost rather than a log that grows
	// for as long as the environment lives. EnvironmentActivitySnapshot's
	// Clients map is bounded for the same reason.
	runtimeUsageHistorySampleCap = 240
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
	FirstObservedAt time.Time `json:"firstObservedAt,omitempty"`
	LastObservedAt  time.Time `json:"lastObservedAt,omitempty"`

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
	Samples []RuntimeUsage `json:"samples,omitempty"`
}

// Latest returns the newest retained sample.
func (h RuntimeUsageHistory) Latest() (RuntimeUsage, bool) {
	if len(h.Samples) == 0 {
		return RuntimeUsage{}, false
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

// AppendRuntimeUsageSample folds one reading into the history. Pure, so the
// retention rules are testable without a container: the caller persists the
// result. now is the time the reading was taken, threaded in explicitly rather
// than read from the sample, since RuntimeUsage (a reading also returned
// verbatim by `erun usage`) carries no timestamp of its own.
func AppendRuntimeUsageSample(history RuntimeUsageHistory, sample RuntimeUsage, now time.Time) RuntimeUsageHistory {
	previous, hasPrevious := history.Latest()
	// A cumulative counter that went backwards can only mean the container was
	// replaced. Inferring the restart from the data needs no second source of
	// truth, and the pod hostname could not supply one anyway: a container
	// restarted in place keeps its pod's name.
	restarted := hasPrevious && runtimeUsageCountersReset(previous, sample)
	if restarted {
		history.Restarts++
	}

	if history.FirstObservedAt.IsZero() {
		history.FirstObservedAt = now
	}
	var elapsed time.Duration
	if !history.LastObservedAt.IsZero() {
		elapsed = now.Sub(history.LastObservedAt)
	}
	if now.After(history.LastObservedAt) {
		history.LastObservedAt = now
	}

	history.ObservedPeakMemoryBytes = max(history.ObservedPeakMemoryBytes, sample.Memory.PeakBytes, sample.Memory.CurrentBytes)

	continuous := hasPrevious && !restarted
	history.ObservedOOMKills += runtimeUsageCounterDelta(previous.Memory.OOMKills, sample.Memory.OOMKills, continuous)
	history.ObservedPeriods += runtimeUsageCounterDelta(previous.CPU.Periods, sample.CPU.Periods, continuous)
	history.ObservedThrottledPeriods += runtimeUsageCounterDelta(previous.CPU.ThrottledPeriods, sample.CPU.ThrottledPeriods, continuous)
	if continuous && elapsed > 0 {
		if milli, ok := runtimeUsageIntervalCPUMilli(previous, sample, elapsed); ok {
			history.ObservedPeakCPUMilli = max(history.ObservedPeakCPUMilli, milli)
		}
	}

	history.Samples = appendBoundedUsageSamples(history.Samples, sample)
	return history
}

// runtimeUsageCounterDelta advances an accumulated total. Across a restart (or
// on the first sample) the counter began at zero, so its whole value is new.
func runtimeUsageCounterDelta(previous, current int64, continuous bool) int64 {
	if !continuous {
		return current
	}
	if current <= previous {
		return 0
	}
	return current - previous
}

func runtimeUsageCountersReset(previous, current RuntimeUsage) bool {
	return current.Memory.PeakBytes < previous.Memory.PeakBytes ||
		current.CPU.UsageUsec < previous.CPU.UsageUsec ||
		current.CPU.Periods < previous.CPU.Periods
}

// runtimeUsageIntervalCPUMilli converts the CPU time burned between two
// readings into millicores over the wall time that elapsed between them.
func runtimeUsageIntervalCPUMilli(previous, current RuntimeUsage, elapsed time.Duration) (int64, bool) {
	usec := current.CPU.UsageUsec - previous.CPU.UsageUsec
	if usec <= 0 {
		return 0, false
	}
	return usec * 1000 / elapsed.Microseconds(), true
}

// appendBoundedUsageSamples keeps the newest samples up to the cap. It copies
// rather than resliceing so the trimmed prefix is not retained by the backing
// array for the life of a long-running monitor.
func appendBoundedUsageSamples(samples []RuntimeUsage, sample RuntimeUsage) []RuntimeUsage {
	next := append(samples, sample)
	if len(next) <= runtimeUsageHistorySampleCap {
		return next
	}
	trimmed := make([]RuntimeUsage, runtimeUsageHistorySampleCap)
	copy(trimmed, next[len(next)-runtimeUsageHistorySampleCap:])
	return trimmed
}

// LoadRuntimeUsageHistory reads an environment's retained usage. A missing or
// unreadable history is "nothing observed yet", not an error: the monitor tick
// that writes it must never fail over its own bookkeeping, and every consumer
// already has to handle an environment with no evidence.
func LoadRuntimeUsageHistory(tenant, environment string) (RuntimeUsageHistory, error) {
	path, err := runtimeUsageHistoryPath(tenant, environment)
	if err != nil {
		return RuntimeUsageHistory{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return RuntimeUsageHistory{}, nil
	}
	var history RuntimeUsageHistory
	if err := json.Unmarshal(data, &history); err != nil {
		return RuntimeUsageHistory{}, nil
	}
	return history, nil
}

func SaveRuntimeUsageHistory(tenant, environment string, history RuntimeUsageHistory) error {
	path, err := runtimeUsageHistoryPath(tenant, environment)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(history)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// runtimeUsageHistoryPath keeps the history beside the activity markers. It has
// to outlive the container that produced it, which is the whole reason it is a
// store and not a live read.
func runtimeUsageHistoryPath(tenant, environment string) (string, error) {
	dir, err := EnvironmentActivityDir(tenant, environment)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, runtimeUsageHistoryFileName), nil
}
