package model

// BuildCgroupMetrics is one step's cgroup cost. It is nil when not applicable and
// has Available false when the read failed.
type BuildCgroupMetrics struct {
	Available         bool    `json:"available"`
	Unavailable       string  `json:"unavailable,omitempty"`
	CPUSeconds        float64 `json:"cpuSeconds,omitempty"`
	CPUPercentOfQuota float64 `json:"cpuPercentOfQuota,omitempty"`
	ThrottledPeriods  int64   `json:"throttledPeriods,omitempty"`
	TotalPeriods      int64   `json:"totalPeriods,omitempty"`
	ThrottledSeconds  float64 `json:"throttledSeconds,omitempty"`
	IOReadBytes       int64   `json:"ioReadBytes,omitempty"`
	IOWriteBytes      int64   `json:"ioWriteBytes,omitempty"`
	PeakMemoryBytes   int64   `json:"peakMemoryBytes,omitempty"`
}
