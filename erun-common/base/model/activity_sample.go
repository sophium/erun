package model

import (
	"time"
)

// ResidentActivitySample is keyed so a recycled pid is not mistaken for its predecessor.
type ResidentActivitySample struct {
	SampledAt time.Time        `json:"sampledAt"`
	CPU       map[string]int64 `json:"cpu,omitempty"`
}

// ResidentActivityResult is one sampler tick's verdict.
type ResidentActivityResult struct {
	Busy      bool                   `json:"busy"`
	Processes []string               `json:"processes,omitempty"`
	Sample    ResidentActivitySample `json:"-"`
}
