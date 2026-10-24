package core

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// newFakeClock reuses fakeClock from deploy_pod_watch_test.go.
func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func TestStepTimingOrdersChildrenByDurationDescending(t *testing.T) {
	clock := newFakeClock()
	root := NewStepTiming("build", clock.now)

	fast := root.Child("fast")
	clock.advance(1 * time.Second)
	fast.Finish(nil)

	slow := root.Child("slow")
	clock.advance(9 * time.Second)
	slow.Finish(nil)

	clock.advance(0)
	root.Finish(nil)

	rows := RenderStepTimingRows(root, 0)
	if len(rows) < 3 {
		t.Fatalf("expected root + 2 children rows, got %d: %v", len(rows), rows)
	}
	if !strings.Contains(rows[1], "slow") {
		t.Fatalf("expected the slower child first (duration-descending), got rows: %v", rows)
	}
	if !strings.Contains(rows[2], "fast") {
		t.Fatalf("expected the faster child second, got rows: %v", rows)
	}
}

func TestStepTimingUnaccountedTimeIsReportedNotDropped(t *testing.T) {
	clock := newFakeClock()
	root := NewStepTiming("release", clock.now)

	child := root.Child("stage-one")
	clock.advance(2 * time.Second)
	child.Finish(nil)

	clock.advance(3 * time.Second)
	root.Finish(nil)

	rows := RenderStepTimingRows(root, 0)
	found := false
	for _, row := range rows {
		if strings.Contains(row, "(unaccounted)") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an (unaccounted) row for the 3s gap, got rows: %v", rows)
	}

	record := root.ToRecord("release")
	if len(record.Steps) != 1 {
		t.Fatalf("expected 1 step in the JSON record, got %d", len(record.Steps))
	}
}

func TestStepTimingJSONRecordCarriesUnaccountedOnParent(t *testing.T) {
	clock := newFakeClock()
	root := NewStepTiming("release", clock.now)

	child := root.Child("stage-one")
	clock.advance(2 * time.Second)
	child.Finish(nil)
	clock.advance(3 * time.Second)
	root.Finish(nil)

	nested := root.toStepJSON()
	if nested.UnaccountedSeconds < 2.9 || nested.UnaccountedSeconds > 3.1 {
		t.Fatalf("expected ~3s unaccounted on the root step JSON, got %v", nested.UnaccountedSeconds)
	}
}

func TestStepTimingConcurrentChildrenReportOverlapNotNegative(t *testing.T) {
	clock := newFakeClock()
	root := NewStepTiming("build", clock.now)

	// Children overlap, as in a concurrent build.
	a := root.Child("amd64")
	b := root.Child("arm64")
	clock.advance(5 * time.Second)
	a.Finish(nil)
	b.Finish(nil)
	root.Finish(nil)

	rows := RenderStepTimingRows(root, 0)
	for _, row := range rows {
		if strings.Contains(row, "(unaccounted)") {
			t.Fatalf("overlapping children must not be reported as unaccounted time: %v", rows)
		}
	}
	found := false
	for _, row := range rows {
		if strings.Contains(row, "ran concurrently") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a concurrent-overlap row, got rows: %v", rows)
	}
}

func TestStepTimingBreaksNoiseLevelTiesByNameNotDuration(t *testing.T) {
	clock := newFakeClock()
	root := NewStepTiming("push", clock.now)

	// Durations within the noise floor must order by name.
	zebra := root.Child("zebra")
	clock.advance(3 * time.Millisecond)
	zebra.Finish(nil)
	apple := root.Child("apple")
	clock.advance(1 * time.Millisecond)
	apple.Finish(nil)
	root.Finish(nil)

	rows := RenderStepTimingRows(root, 0)
	appleIdx, zebraIdx := -1, -1
	for i, row := range rows {
		if strings.Contains(row, "apple") {
			appleIdx = i
		}
		if strings.Contains(row, "zebra") {
			zebraIdx = i
		}
	}
	if appleIdx == -1 || zebraIdx == -1 || appleIdx > zebraIdx {
		t.Fatalf("expected apple before zebra (alphabetical tie-break under the noise floor), got rows: %v", rows)
	}
}

func TestStepTimingReportsFailureAndError(t *testing.T) {
	clock := newFakeClock()
	root := NewStepTiming("push", clock.now)
	child := root.Child("erun-console")
	clock.advance(1 * time.Second)
	child.Finish(errors.New("boom"))
	root.Finish(errors.New("boom"))

	rows := RenderStepTimingRows(root, 0)
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "(failed)") {
		t.Fatalf("expected a (failed) marker in the rendered table, got:\n%s", joined)
	}
	if !strings.Contains(joined, "boom") {
		t.Fatalf("expected the error text in the rendered table, got:\n%s", joined)
	}

	record := root.ToRecord("push")
	if !record.Failed {
		t.Fatalf("expected record.Failed to be true")
	}
	if record.Error != "boom" {
		t.Fatalf("expected record.Error = boom, got %q", record.Error)
	}
	if !record.Steps[0].Failed || record.Steps[0].Error != "boom" {
		t.Fatalf("expected the child step to carry the failure too, got %+v", record.Steps[0])
	}
}

func TestStepTimingCacheDecisionAppearsOnStepAndPlatformChildren(t *testing.T) {
	clock := newFakeClock()
	root := NewStepTiming("build", clock.now)
	image := root.Child("erun-console")
	cache := &CacheDecision{Hit: false, MissReason: "fingerprint image is missing for platform linux/amd64"}
	image.setCache(cache)
	clock.advance(2 * time.Second)
	image.AddFinishedChild("linux/amd64", 2*time.Second, nil, cache, nil)
	clock.advance(1 * time.Second)
	image.AddFinishedChild("linux/arm64", 1*time.Second, nil, cache, nil)
	image.Finish(nil)
	root.Finish(nil)

	rows := RenderStepTimingRows(root, 0)
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "cache miss: fingerprint image is missing for platform linux/amd64") {
		t.Fatalf("expected the miss reason on the image row, got:\n%s", joined)
	}
	if strings.Count(joined, "cache miss:") < 3 {
		t.Fatalf("expected the cache tag on the image row and both platform rows, got:\n%s", joined)
	}

	record := root.ToRecord("build")
	imageJSON := record.Steps[0]
	if imageJSON.CacheHit == nil || *imageJSON.CacheHit {
		t.Fatalf("expected the image step to record a cache miss, got %+v", imageJSON.CacheHit)
	}
	if len(imageJSON.Steps) != 2 {
		t.Fatalf("expected 2 platform children in the JSON record, got %d", len(imageJSON.Steps))
	}
	for _, platform := range imageJSON.Steps {
		if platform.CacheHit == nil || *platform.CacheHit {
			t.Fatalf("expected platform %s to carry the same cache-miss tag, got %+v", platform.Name, platform.CacheHit)
		}
	}
}

func TestStepTimingRecordMarshalsToJSON(t *testing.T) {
	clock := newFakeClock()
	root := NewStepTiming("release", clock.now)
	stage := root.Child("sync-remote")
	clock.advance(500 * time.Millisecond)
	stage.Finish(nil)
	root.Finish(nil)

	record := root.ToRecord("release")
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal timing record: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal timing record: %v", err)
	}
	if decoded["command"] != "release" {
		t.Fatalf("expected command=release in the JSON, got %v", decoded["command"])
	}
	steps, ok := decoded["steps"].([]any)
	if !ok || len(steps) != 1 {
		t.Fatalf("expected exactly 1 step in the JSON record, got %v", decoded["steps"])
	}
}

func TestContextStartTimingStepIsNoopWithoutAnActiveRoot(t *testing.T) {
	ctx := Context{}
	next, finish := ctx.StartTimingStep("whatever")
	if next.Timing != nil {
		t.Fatalf("expected no timing root to be created outside an active run")
	}
	finish(errors.New("should not panic"))
}

func TestContextStartTimingStepNestsUnderActiveRoot(t *testing.T) {
	clock := newFakeClock()
	ctx := Context{}
	ctx.Timing = NewStepTiming("build", clock.now)

	stepCtx, finish := ctx.StartTimingStep("erun-console")
	if stepCtx.Timing == nil || stepCtx.Timing == ctx.Timing {
		t.Fatalf("expected startTimingStep to scope the returned context to a new child")
	}
	clock.advance(time.Second)
	finish(nil)

	if len(ctx.Timing.Children) != 1 {
		t.Fatalf("expected the child to attach to the root, got %d children", len(ctx.Timing.Children))
	}
}

func TestTimingRecordFileNameHasNoColonsForWindowsSafety(t *testing.T) {
	name := TimingRecordFileName("release", time.Date(2026, 8, 22, 14, 5, 12, 0, time.UTC))
	if strings.ContainsAny(name, ":") {
		t.Fatalf("timing record file names must be Windows-safe, got %q", name)
	}
	if !strings.HasPrefix(name, "release-") || !strings.HasSuffix(name, ".json") {
		t.Fatalf("unexpected timing record file name shape: %q", name)
	}
}
