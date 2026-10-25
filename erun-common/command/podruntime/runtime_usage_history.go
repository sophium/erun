package podruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/deploy/runtimeusage"
)

const (

	// runtimeUsageHistorySampleCap is roughly two hours at the monitor's 30s tick.
	runtimeUsageHistorySampleCap = 240
)

// AppendRuntimeUsageSample folds one reading into the history; RuntimeUsage carries no
// timestamp, so now is passed in.
func AppendRuntimeUsageSample(history runtimeusage.RuntimeUsageHistory, sample model.RuntimeUsage, now time.Time) runtimeusage.RuntimeUsageHistory {
	previous, hasPrevious := history.Latest()
	// A cumulative counter going backwards means the container was replaced; an in-place
	// restart keeps the pod name, so the hostname cannot tell.
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

// Across a restart or on the first sample the counter began at zero.
func runtimeUsageCounterDelta(previous, current int64, continuous bool) int64 {
	if !continuous {
		return current
	}
	if current <= previous {
		return 0
	}
	return current - previous
}

func runtimeUsageCountersReset(previous, current model.RuntimeUsage) bool {
	return current.Memory.PeakBytes < previous.Memory.PeakBytes ||
		current.CPU.UsageUsec < previous.CPU.UsageUsec ||
		current.CPU.Periods < previous.CPU.Periods
}

func runtimeUsageIntervalCPUMilli(previous, current model.RuntimeUsage, elapsed time.Duration) (int64, bool) {
	usec := current.CPU.UsageUsec - previous.CPU.UsageUsec
	if usec <= 0 {
		return 0, false
	}
	return usec * 1000 / elapsed.Microseconds(), true
}

// Copy rather than reslice so the trimmed prefix is not retained by the backing array.
func appendBoundedUsageSamples(samples []model.RuntimeUsage, sample model.RuntimeUsage) []model.RuntimeUsage {
	samples = append(samples, sample)
	if len(samples) <= runtimeUsageHistorySampleCap {
		return samples
	}
	trimmed := make([]model.RuntimeUsage, runtimeUsageHistorySampleCap)
	copy(trimmed, samples[len(samples)-runtimeUsageHistorySampleCap:])
	return trimmed
}

func SaveRuntimeUsageHistory(tenant, environment string, history runtimeusage.RuntimeUsageHistory) error {
	path, err := runtimeusage.RuntimeUsageHistoryPath(tenant, environment)
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
