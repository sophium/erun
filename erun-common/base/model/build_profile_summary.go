package model

// BuildProfileStepSummary names a step by its full path so same-named steps under
// different parents stay distinct.
type BuildProfileStepSummary struct {
	Name            string              `json:"name"`
	DurationSeconds float64             `json:"durationSeconds"`
	Cgroup          *BuildCgroupMetrics `json:"cgroup,omitempty"`
}

// BuildProfileSummary is the bounded profile a build reports with its outcome.
// TotalStepCount tells whether steps were dropped.
type BuildProfileSummary struct {
	DurationSeconds    float64                   `json:"durationSeconds"`
	Failed             bool                      `json:"failed,omitempty"`
	Cgroup             *BuildCgroupMetrics       `json:"cgroup,omitempty"`
	TopSteps           []BuildProfileStepSummary `json:"topSteps,omitempty"`
	TotalStepCount     int                       `json:"totalStepCount,omitempty"`
	TruncatedStepCount int                       `json:"truncatedStepCount,omitempty"`
}
