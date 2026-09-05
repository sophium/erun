package eruncommon

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// newFakeClock reuses the fakeClock defined in deploy_pod_watch_test.go —
// timing is awkward to test deterministically, so both suites control
// elapsed time exactly instead of sleeping.
func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func TestStepTimingOrdersChildrenByDurationDescending(t *testing.T) {
	clock := newFakeClock()
	root := newStepTiming("build", clock.now)

	fast := root.child("fast")
	clock.advance(1 * time.Second)
	fast.finish(nil)

	slow := root.child("slow")
	clock.advance(9 * time.Second)
	slow.finish(nil)

	clock.advance(0)
	root.finish(nil)

	rows := renderStepTimingRows(root, 0)
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
	root := newStepTiming("release", clock.now)

	child := root.child("stage-one")
	clock.advance(2 * time.Second)
	child.finish(nil)

	// A gap the child didn't cover — e.g. time spent between stages.
	clock.advance(3 * time.Second)
	root.finish(nil)

	rows := renderStepTimingRows(root, 0)
	found := false
	for _, row := range rows {
		if strings.Contains(row, "(unaccounted)") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an (unaccounted) row for the 3s gap, got rows: %v", rows)
	}

	record := root.toRecord("release")
	if len(record.Steps) != 1 {
		t.Fatalf("expected 1 step in the JSON record, got %d", len(record.Steps))
	}
}

// UnaccountedSeconds lives on TimingStepJSON, so exercise it there directly.
func TestStepTimingJSONRecordCarriesUnaccountedOnParent(t *testing.T) {
	clock := newFakeClock()
	root := newStepTiming("release", clock.now)

	child := root.child("stage-one")
	clock.advance(2 * time.Second)
	child.finish(nil)
	clock.advance(3 * time.Second)
	root.finish(nil)

	nested := root.toStepJSON()
	if nested.UnaccountedSeconds < 2.9 || nested.UnaccountedSeconds > 3.1 {
		t.Fatalf("expected ~3s unaccounted on the root step JSON, got %v", nested.UnaccountedSeconds)
	}
}

func TestStepTimingConcurrentChildrenReportOverlapNotNegative(t *testing.T) {
	clock := newFakeClock()
	root := newStepTiming("build", clock.now)

	// Two children whose combined duration exceeds the parent's own wall time
	// — the shape a concurrent build wave produces.
	a := root.child("amd64")
	b := root.child("arm64")
	clock.advance(5 * time.Second)
	a.finish(nil)
	b.finish(nil)
	root.finish(nil)

	rows := renderStepTimingRows(root, 0)
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
	root := newStepTiming("push", clock.now)

	// Both children finish within the noise floor of each other — the shape a
	// near-instant stub produces — so the sort must not depend on which one
	// happened to measure a few microseconds larger, or the same run could
	// render a different order every time.
	zebra := root.child("zebra")
	clock.advance(3 * time.Millisecond)
	zebra.finish(nil)
	apple := root.child("apple")
	clock.advance(1 * time.Millisecond)
	apple.finish(nil)
	root.finish(nil)

	rows := renderStepTimingRows(root, 0)
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
	root := newStepTiming("push", clock.now)
	child := root.child("erun-console")
	clock.advance(1 * time.Second)
	child.finish(errors.New("boom"))
	root.finish(errors.New("boom"))

	rows := renderStepTimingRows(root, 0)
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "(failed)") {
		t.Fatalf("expected a (failed) marker in the rendered table, got:\n%s", joined)
	}
	if !strings.Contains(joined, "boom") {
		t.Fatalf("expected the error text in the rendered table, got:\n%s", joined)
	}

	record := root.toRecord("push")
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
	root := newStepTiming("build", clock.now)
	image := root.child("erun-console")
	image.setCache(false, "fingerprint image is missing for platform linux/amd64")

	cache := &cacheDecision{hit: false, missReason: "fingerprint image is missing for platform linux/amd64"}
	clock.advance(2 * time.Second)
	image.addFinishedChild("linux/amd64", 2*time.Second, nil, cache)
	clock.advance(1 * time.Second)
	image.addFinishedChild("linux/arm64", 1*time.Second, nil, cache)
	image.finish(nil)
	root.finish(nil)

	rows := renderStepTimingRows(root, 0)
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "cache miss: fingerprint image is missing for platform linux/amd64") {
		t.Fatalf("expected the miss reason on the image row, got:\n%s", joined)
	}
	if strings.Count(joined, "cache miss:") < 3 {
		t.Fatalf("expected the cache tag on the image row and both platform rows, got:\n%s", joined)
	}

	record := root.toRecord("build")
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
	root := newStepTiming("release", clock.now)
	stage := root.child("sync-remote")
	clock.advance(500 * time.Millisecond)
	stage.finish(nil)
	root.finish(nil)

	record := root.toRecord("release")
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

func TestIncrementalCacheDecisionMatchesTraceReasons(t *testing.T) {
	cases := []struct {
		name       string
		build      DockerBuildSpec
		applicable bool
		hit        bool
		reason     string
	}{
		{
			name:       "no fingerprint computed",
			build:      DockerBuildSpec{},
			applicable: false,
		},
		{
			name:       "promoted from cache",
			build:      DockerBuildSpec{Fingerprint: "abc", Promote: true},
			applicable: true,
			hit:        true,
		},
		{
			name:       "cascade rebuild",
			build:      DockerBuildSpec{Fingerprint: "abc", CascadeRebuildFromTag: "erun-devops:1.0.0"},
			applicable: true,
			hit:        false,
			reason:     "dependency erun-devops:1.0.0 is rebuilding",
		},
		{
			name:       "missing platforms",
			build:      DockerBuildSpec{Fingerprint: "abc", MissingFingerprintPlatforms: []string{"linux/amd64"}},
			applicable: true,
			hit:        false,
			reason:     "fingerprint image is missing for platform linux/amd64",
		},
		{
			name:       "no cached image at all",
			build:      DockerBuildSpec{Fingerprint: "abc"},
			applicable: true,
			hit:        false,
			reason:     "no cached fingerprint image",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hit, applicable, reason := incrementalCacheDecision(tc.build)
			if applicable != tc.applicable {
				t.Fatalf("applicable = %v, want %v", applicable, tc.applicable)
			}
			if !applicable {
				return
			}
			if hit != tc.hit {
				t.Fatalf("hit = %v, want %v", hit, tc.hit)
			}
			if reason != tc.reason {
				t.Fatalf("reason = %q, want %q", reason, tc.reason)
			}
		})
	}
}

func TestContextStartTimingStepIsNoopWithoutAnActiveRoot(t *testing.T) {
	ctx := Context{}
	next, finish := ctx.startTimingStep("whatever")
	if next.timing != nil {
		t.Fatalf("expected no timing root to be created outside an active run")
	}
	finish(errors.New("should not panic"))
}

func TestContextStartTimingStepNestsUnderActiveRoot(t *testing.T) {
	clock := newFakeClock()
	ctx := Context{}
	ctx.timing = newStepTiming("build", clock.now)

	stepCtx, finish := ctx.startTimingStep("erun-console")
	if stepCtx.timing == nil || stepCtx.timing == ctx.timing {
		t.Fatalf("expected startTimingStep to scope the returned context to a new child")
	}
	clock.advance(time.Second)
	finish(nil)

	if len(ctx.timing.children) != 1 {
		t.Fatalf("expected the child to attach to the root, got %d children", len(ctx.timing.children))
	}
}

func TestTimingRecordFileNameHasNoColonsForWindowsSafety(t *testing.T) {
	name := timingRecordFileName("release", time.Date(2026, 8, 22, 14, 5, 12, 0, time.UTC))
	if strings.ContainsAny(name, ":") {
		t.Fatalf("timing record file names must be Windows-safe, got %q", name)
	}
	if !strings.HasPrefix(name, "release-") || !strings.HasSuffix(name, ".json") {
		t.Fatalf("unexpected timing record file name shape: %q", name)
	}
}
