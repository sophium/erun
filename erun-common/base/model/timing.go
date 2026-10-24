package model

import (
	"time"
)

// TimingRecord is the per-run JSON counterpart to the timing table.
type TimingRecord struct {
	Command         string              `json:"command"`
	StartedAt       time.Time           `json:"startedAt"`
	DurationSeconds float64             `json:"durationSeconds"`
	Duration        string              `json:"duration"`
	Failed          bool                `json:"failed"`
	Error           string              `json:"error,omitempty"`
	Cgroup          *BuildCgroupMetrics `json:"cgroup,omitempty"`
	Steps           []TimingStepJSON    `json:"steps,omitempty"`
}

// TimingStepJSON keeps children in insertion order so two records diff cleanly.
type TimingStepJSON struct {
	Name               string              `json:"name"`
	DurationSeconds    float64             `json:"durationSeconds"`
	Duration           string              `json:"duration"`
	Failed             bool                `json:"failed,omitempty"`
	Error              string              `json:"error,omitempty"`
	CacheHit           *bool               `json:"cacheHit,omitempty"`
	CacheMissReason    string              `json:"cacheMissReason,omitempty"`
	UnaccountedSeconds float64             `json:"unaccountedSeconds,omitempty"`
	OverlapSeconds     float64             `json:"overlapSeconds,omitempty"`
	Cgroup             *BuildCgroupMetrics `json:"cgroup,omitempty"`
	Steps              []TimingStepJSON    `json:"steps,omitempty"`
}
