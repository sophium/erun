package runtimeusage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
)

// RuntimeUsageHistory keeps monotonic aggregates that survive container
// restarts (which reset memory.peak) and the rolling Samples window.
type RuntimeUsageHistory struct {
	// FirstObservedAt anchors the evidence window; shrinking is gated on its span.
	FirstObservedAt time.Time `json:"firstObservedAt,omitzero"`
	LastObservedAt  time.Time `json:"lastObservedAt,omitzero"`

	ObservedPeakMemoryBytes int64 `json:"observedPeakMemoryBytes,omitempty"`
	// ObservedOOMKills sums deltas because memory.events resets with the container.
	ObservedOOMKills int64 `json:"observedOomKills,omitempty"`

	// ObservedPeakCPUMilli is the highest cpu.stat rate between two ticks (not
	// loadavg); shorter bursts are flattened, so shrinking keeps a multiple of it.
	ObservedPeakCPUMilli int64 `json:"observedPeakCpuMilli,omitempty"`
	// ObservedPeriods and ObservedThrottledPeriods accumulate across restarts.
	ObservedPeriods          int64 `json:"observedPeriods,omitempty"`
	ObservedThrottledPeriods int64 `json:"observedThrottledPeriods,omitempty"`

	// Restarts is inferred from a counter going backwards.
	Restarts int `json:"restarts,omitempty"`

	// Samples is never used to recompute the aggregates.
	Samples []model.RuntimeUsage `json:"samples,omitempty"`
}

// Latest returns the newest retained sample.
func (h RuntimeUsageHistory) Latest() (model.RuntimeUsage, bool) {
	if len(h.Samples) == 0 {
		return model.RuntimeUsage{}, false
	}
	return h.Samples[len(h.Samples)-1], true
}

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

const (
	runtimeUsageHistoryFileName = "usage-history.json"
)

// LoadRuntimeUsageHistory treats a missing or unreadable history as empty so
// the monitor tick never fails over its own bookkeeping.
func LoadRuntimeUsageHistory(tenant, environment string) (RuntimeUsageHistory, error) {
	path, err := RuntimeUsageHistoryPath(tenant, environment)
	if err != nil {
		return RuntimeUsageHistory{}, err
	}
	return readRuntimeUsageHistory(path), nil
}

func readRuntimeUsageHistory(path string) RuntimeUsageHistory {
	data, err := os.ReadFile(path)
	if err != nil {
		return RuntimeUsageHistory{}
	}
	var history RuntimeUsageHistory
	if err := json.Unmarshal(data, &history); err != nil {
		return RuntimeUsageHistory{}
	}
	return history
}

// RuntimeUsageHistoryPath is beside the activity markers so it outlives the container.
func RuntimeUsageHistoryPath(tenant, environment string) (string, error) {
	dir, err := activity.EnvironmentActivityDir(tenant, environment)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, runtimeUsageHistoryFileName), nil
}
