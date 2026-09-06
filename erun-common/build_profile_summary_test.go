package eruncommon

import (
	"fmt"
	"testing"
)

func TestSummarizeTimingRecordForProfileKeepsWholeTreeUnderTheCap(t *testing.T) {
	record := TimingRecord{
		DurationSeconds: 10,
		Failed:          false,
		Steps: []TimingStepJSON{
			{Name: "fast-image", DurationSeconds: 1},
			{
				Name:            "slow-image",
				DurationSeconds: 9,
				Steps: []TimingStepJSON{
					{Name: "linux/amd64", DurationSeconds: 5},
					{Name: "linux/arm64", DurationSeconds: 4},
				},
			},
		},
	}

	summary := SummarizeTimingRecordForProfile(record)

	if summary.DurationSeconds != 10 {
		t.Errorf("expected DurationSeconds to carry the record's own total, got %v", summary.DurationSeconds)
	}
	if summary.TotalStepCount != 4 {
		t.Fatalf("expected 4 flattened steps (fast-image, slow-image, and its 2 children), got %d: %+v", summary.TotalStepCount, summary.TopSteps)
	}
	if summary.TruncatedStepCount != 0 {
		t.Errorf("expected no truncation under the cap, got %d", summary.TruncatedStepCount)
	}
	if len(summary.TopSteps) != 4 {
		t.Fatalf("expected all 4 steps kept, got %d", len(summary.TopSteps))
	}
	if summary.TopSteps[0].Name != "slow-image" || summary.TopSteps[0].DurationSeconds != 9 {
		t.Errorf("expected the costliest step first, got %+v", summary.TopSteps[0])
	}
	if summary.TopSteps[1].Name != "slow-image > linux/amd64" {
		t.Errorf("expected a nested step's name to carry its ancestry path, got %q", summary.TopSteps[1].Name)
	}
}

func TestSummarizeTimingRecordForProfileBoundsToTopNCostliestSteps(t *testing.T) {
	var steps []TimingStepJSON
	for i := 0; i < buildProfileTopStepCount+5; i++ {
		steps = append(steps, TimingStepJSON{
			Name:            fmt.Sprintf("step-%02d", i),
			DurationSeconds: float64(i), // step-24 is costliest, step-00 cheapest
		})
	}
	record := TimingRecord{DurationSeconds: 100, Steps: steps}

	summary := SummarizeTimingRecordForProfile(record)

	if summary.TotalStepCount != buildProfileTopStepCount+5 {
		t.Fatalf("expected TotalStepCount to reflect every step, got %d", summary.TotalStepCount)
	}
	if len(summary.TopSteps) != buildProfileTopStepCount {
		t.Fatalf("expected TopSteps bounded to %d, got %d", buildProfileTopStepCount, len(summary.TopSteps))
	}
	if summary.TruncatedStepCount != 5 {
		t.Errorf("expected 5 steps reported truncated, got %d", summary.TruncatedStepCount)
	}
	for i := 1; i < len(summary.TopSteps); i++ {
		if summary.TopSteps[i-1].DurationSeconds < summary.TopSteps[i].DurationSeconds {
			t.Fatalf("expected TopSteps sorted by duration descending, got %+v", summary.TopSteps)
		}
	}
	wantCostliest := fmt.Sprintf("step-%02d", buildProfileTopStepCount+4)
	if summary.TopSteps[0].Name != wantCostliest {
		t.Errorf("expected the single costliest step %q first, got %q", wantCostliest, summary.TopSteps[0].Name)
	}
}

func TestSummarizeTimingRecordForProfileHandlesNoSteps(t *testing.T) {
	summary := SummarizeTimingRecordForProfile(TimingRecord{DurationSeconds: 1, Failed: true})

	if summary.TotalStepCount != 0 || len(summary.TopSteps) != 0 || summary.TruncatedStepCount != 0 {
		t.Fatalf("expected an empty step tree to summarize to no steps, got %+v", summary)
	}
	if !summary.Failed {
		t.Errorf("expected Failed to carry through from the record")
	}
}
