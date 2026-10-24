package podruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

const (

	// runtimeUsageHistorySampleCap bounds the rolling window of raw samples.
	// The monitor ticks every 30s, so this is roughly two hours of readings —
	// enough to see recent rates, and a fixed cost rather than a log that grows
	// for as long as the environment lives. EnvironmentActivitySnapshot's
	// Clients map is bounded for the same reason.
	runtimeUsageHistorySampleCap = 240
)

// AppendRuntimeUsageSample folds one reading into the history. Pure, so the
// retention rules are testable without a container: the caller persists the
// result. now is the time the reading was taken, threaded in explicitly rather
// than read from the sample, since RuntimeUsage (a reading also returned
// verbatim by `erun usage`) carries no timestamp of its own.
func AppendRuntimeUsageSample(history deploy.RuntimeUsageHistory, sample config.RuntimeUsage, now time.Time) deploy.RuntimeUsageHistory {
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

func runtimeUsageCountersReset(previous, current config.RuntimeUsage) bool {
	return current.Memory.PeakBytes < previous.Memory.PeakBytes ||
		current.CPU.UsageUsec < previous.CPU.UsageUsec ||
		current.CPU.Periods < previous.CPU.Periods
}

// runtimeUsageIntervalCPUMilli converts the CPU time burned between two
// readings into millicores over the wall time that elapsed between them.
func runtimeUsageIntervalCPUMilli(previous, current config.RuntimeUsage, elapsed time.Duration) (int64, bool) {
	usec := current.CPU.UsageUsec - previous.CPU.UsageUsec
	if usec <= 0 {
		return 0, false
	}
	return usec * 1000 / elapsed.Microseconds(), true
}

// appendBoundedUsageSamples keeps the newest samples up to the cap. It copies
// rather than resliceing so the trimmed prefix is not retained by the backing
// array for the life of a long-running monitor.
func appendBoundedUsageSamples(samples []config.RuntimeUsage, sample config.RuntimeUsage) []config.RuntimeUsage {
	next := append(samples, sample)
	if len(next) <= runtimeUsageHistorySampleCap {
		return next
	}
	trimmed := make([]config.RuntimeUsage, runtimeUsageHistorySampleCap)
	copy(trimmed, next[len(next)-runtimeUsageHistorySampleCap:])
	return trimmed
}

func SaveRuntimeUsageHistory(tenant, environment string, history deploy.RuntimeUsageHistory) error {
	path, err := deploy.RuntimeUsageHistoryPath(tenant, environment)
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
