package model

// RuntimeSizingAction is the direction a recommendation points.
type RuntimeSizingAction string

const (
	RuntimeSizingHold  RuntimeSizingAction = "hold"
	RuntimeSizingRaise RuntimeSizingAction = "raise"
	RuntimeSizingLower RuntimeSizingAction = "lower"
	// RuntimeSizingUnknown differs from hold, which means erun looked and the size is right.
	RuntimeSizingUnknown RuntimeSizingAction = "insufficient-evidence"
)

// RuntimeSizingConfidence is high only for raises: harm already done, not an argument from silence.
type RuntimeSizingConfidence string

const (
	RuntimeSizingConfidenceHigh RuntimeSizingConfidence = "high"
	RuntimeSizingConfidenceLow  RuntimeSizingConfidence = "low"
)

// RuntimeSizingVerdict is one resource's standing recommendation.
type RuntimeSizingVerdict struct {
	Resource   string                  `json:"resource"`
	Action     RuntimeSizingAction     `json:"action"`
	Current    string                  `json:"current,omitempty"`
	Suggested  string                  `json:"suggested,omitempty"`
	Confidence RuntimeSizingConfidence `json:"confidence,omitempty"`
	Reason     string                  `json:"reason"`
}

// RuntimeSizingRecommendation never applies itself: resizing mid-run is worse than any failure it prevents.
type RuntimeSizingRecommendation struct {
	// Knob is always runtimepod; NamespaceQuota caps the namespace and resizes nothing.
	Knob     string                 `json:"knob"`
	Verdicts []RuntimeSizingVerdict `json:"verdicts"`
	Evidence RuntimeSizingEvidence  `json:"evidence"`
}

// RuntimeSizingEvidence is what the recommendation was derived from, so a reader can disagree on the merits.
type RuntimeSizingEvidence struct {
	ObservedSeconds int64 `json:"observedSeconds"`
	Samples         int   `json:"samples"`
	// memory.peak resets on restart, so a peak surviving restarts was retained by erun.
	Restarts int `json:"restarts"`

	MemoryLimitBytes        int64 `json:"memoryLimitBytes,omitempty"`
	ObservedPeakMemoryBytes int64 `json:"observedPeakMemoryBytes,omitempty"`
	ObservedOOMKills        int64 `json:"observedOomKills"`

	CPUQuotaMilli            int64 `json:"cpuQuotaMilli,omitempty"`
	ObservedPeakCPUMilli     int64 `json:"observedPeakCpuMilli,omitempty"`
	ObservedPeriods          int64 `json:"observedPeriods,omitempty"`
	ObservedThrottledPeriods int64 `json:"observedThrottledPeriods,omitempty"`

	// Nothing here is loadavg-derived: loadavg counts the host's run queue, not this container's throttling.
	Signals     []string `json:"signals,omitempty"`
	Unavailable []string `json:"unavailable,omitempty"`
}
