package core

import (
	"sort"
)

// buildProfileTopStepCount bounds the profile a build reports to the platform,
// however deep the step tree grows.
const buildProfileTopStepCount = 10

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

// SummarizeTimingRecordForProfile keeps the buildProfileTopStepCount longest
// steps from every depth of record's tree.
func SummarizeTimingRecordForProfile(record TimingRecord) BuildProfileSummary {
	flattened := flattenTimingStepJSONForProfile(record.Steps, "")
	sort.SliceStable(flattened, func(i, j int) bool {
		return flattened[i].DurationSeconds > flattened[j].DurationSeconds
	})

	summary := BuildProfileSummary{
		DurationSeconds: record.DurationSeconds,
		Failed:          record.Failed,
		Cgroup:          record.Cgroup,
		TotalStepCount:  len(flattened),
	}
	if len(flattened) > buildProfileTopStepCount {
		summary.TopSteps = flattened[:buildProfileTopStepCount]
		summary.TruncatedStepCount = len(flattened) - buildProfileTopStepCount
	} else {
		summary.TopSteps = flattened
	}
	return summary
}

func flattenTimingStepJSONForProfile(steps []TimingStepJSON, ancestryPath string) []BuildProfileStepSummary {
	var flattened []BuildProfileStepSummary
	for _, step := range steps {
		name := step.Name
		if ancestryPath != "" {
			name = ancestryPath + " > " + step.Name
		}
		flattened = append(flattened, BuildProfileStepSummary{
			Name:            name,
			DurationSeconds: step.DurationSeconds,
			Cgroup:          step.Cgroup,
		})
		flattened = append(flattened, flattenTimingStepJSONForProfile(step.Steps, name)...)
	}
	return flattened
}
