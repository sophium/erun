package core

import (
	"sort"

	"github.com/sophium/erun/erun-common/base/model"
)

// buildProfileTopStepCount bounds the profile a build reports to the platform,
// however deep the step tree grows.
const buildProfileTopStepCount = 10

// SummarizeTimingRecordForProfile keeps the buildProfileTopStepCount longest
// steps from every depth of record's tree.
func SummarizeTimingRecordForProfile(record model.TimingRecord) model.BuildProfileSummary {
	flattened := flattenTimingStepJSONForProfile(record.Steps, "")
	sort.SliceStable(flattened, func(i, j int) bool {
		return flattened[i].DurationSeconds > flattened[j].DurationSeconds
	})

	summary := model.BuildProfileSummary{
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

func flattenTimingStepJSONForProfile(steps []model.TimingStepJSON, ancestryPath string) []model.BuildProfileStepSummary {
	var flattened []model.BuildProfileStepSummary
	for _, step := range steps {
		name := step.Name
		if ancestryPath != "" {
			name = ancestryPath + " > " + step.Name
		}
		flattened = append(flattened, model.BuildProfileStepSummary{
			Name:            name,
			DurationSeconds: step.DurationSeconds,
			Cgroup:          step.Cgroup,
		})
		flattened = append(flattened, flattenTimingStepJSONForProfile(step.Steps, name)...)
	}
	return flattened
}
