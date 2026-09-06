package eruncommon

import "sort"

// build_profile_summary.go bounds a build's step-timing tree down to a
// payload small enough to carry alongside ReportBuildOutcome's self-report to
// the erun platform -- the operator ask was "go to builds, select a build,
// see what consumed CPU or hit an I/O bottleneck", which needs the timing
// tree that already lands in ~/.erun/timing/build-*.json to also reach the
// platform the desktop reads.
//
// A gate build's step tree is small today (image -> platform), but breaking
// erun-devops into its Dockerfile stages and `make check` phases has been
// proposed and could turn that into dozens of rows. Rather than carry the
// full tree, BuildProfileSummary stores the build's own totals plus the
// buildProfileTopStepCount costliest steps by duration -- bounded regardless
// of how deep a future step tree grows.
const buildProfileTopStepCount = 10

// BuildCgroupProfileMetrics is one step's CPU/throttling/I/O cost. It mirrors
// the shape a build cgroup collector reports (cpu seconds against quota,
// throttled/total periods, read/write bytes, peak memory) -- see the
// "Assumptions" note in this feature's PR description for why no such
// collector exists on this branch yet: SummarizeTimingRecordForProfile has no
// source to populate this from today, so every step summary's Cgroup is nil
// until that collector lands and a follow-up change wires its output in
// here. Available distinguishes "no cgroup to have an opinion about" (the
// whole field is nil) from "a cgroup exists but its counters could not be
// read" (Available: false, Unavailable: reason) -- a build outside a runtime
// pod is the former, a pod-injected build whose remote read failed is the
// latter.
type BuildCgroupProfileMetrics struct {
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

// BuildProfileStepSummary is one entry in BuildProfileSummary's bounded
// top-N costliest-steps list. Name is the step's full path (parent > child)
// so two same-named steps under different parents (e.g. two images both
// building "linux/amd64") stay distinguishable once flattened out of the
// tree.
type BuildProfileStepSummary struct {
	Name            string                     `json:"name"`
	DurationSeconds float64                    `json:"durationSeconds"`
	Cgroup          *BuildCgroupProfileMetrics `json:"cgroup,omitempty"`
}

// BuildProfileSummary is the bounded profile a build self-reports alongside
// its outcome (see ReportBuildOutcomeParams.Profile). TotalStepCount is the
// number of steps the full tree actually had, so a caller can tell "this
// build had few enough steps that TopSteps is the whole tree" from "steps
// were dropped" without needing TruncatedStepCount to be nonzero.
type BuildProfileSummary struct {
	DurationSeconds    float64                    `json:"durationSeconds"`
	Failed             bool                       `json:"failed,omitempty"`
	Cgroup             *BuildCgroupProfileMetrics `json:"cgroup,omitempty"`
	TopSteps           []BuildProfileStepSummary  `json:"topSteps,omitempty"`
	TotalStepCount     int                        `json:"totalStepCount,omitempty"`
	TruncatedStepCount int                        `json:"truncatedStepCount,omitempty"`
}

// SummarizeTimingRecordForProfile flattens record's step tree (at every
// depth) into one list, sorts it by duration descending, and keeps only the
// costliest buildProfileTopStepCount -- the same ordering
// RenderTimingRecordRows-style tooling already uses to put the dominant cost
// first, applied here to decide what gets kept rather than just what gets
// shown first.
func SummarizeTimingRecordForProfile(record TimingRecord) BuildProfileSummary {
	flattened := flattenTimingStepJSONForProfile(record.Steps, "")
	sort.SliceStable(flattened, func(i, j int) bool {
		return flattened[i].DurationSeconds > flattened[j].DurationSeconds
	})

	summary := BuildProfileSummary{
		DurationSeconds: record.DurationSeconds,
		Failed:          record.Failed,
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

// flattenTimingStepJSONForProfile walks steps recursively, naming each
// flattened entry with its full ancestry path so siblings that share a leaf
// name (two images each building "linux/amd64") stay distinguishable once
// they are no longer nested.
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
		})
		flattened = append(flattened, flattenTimingStepJSONForProfile(step.Steps, name)...)
	}
	return flattened
}
