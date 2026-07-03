package eruncommon

import (
	"testing"
	"time"
)

// TestFreezeNow pins the mechanism that keeps a whole deploy on a single
// snapshot timestamp. The local snapshot version is minted from now() at several
// independent sites — the per-chart build (ResolveDockerBuildForComponent), the
// cwd "current build" (resolveCurrentDockerComponentBuildForDeploy), and each
// image's version (resolveDockerImageVersion). The resolution entrypoints
// (ResolveCurrentDeploySpecs, resolveDeploySpecForOpenResult,
// ResolveOpenRuntimeDeploySpec) call freezeNow once, above the per-spec work, so
// every one of those sites observes the same instant. Without it a multi-image /
// multi-spec deploy stamps images with timestamps that drift apart and the
// runtime chart's persisted RuntimeVersion can end up being a tag that was never
// built or pushed — a phantom version the deploy picker can never offer because
// it gates on registry presence.
//
// The end-to-end property (one timestamp across a real multi-image deploy) is
// not observable from the dry-run integration binary: it uses the real clock so
// independent now() calls land in the same wall-clock second, and the golden
// normalizer collapses every snapshot version to <VERSION>, so a golden cannot
// tell a single timestamp from drifting ones. This white-box test owns the
// contract instead, mirroring deploy_persist_runtime_version_test.go.
func TestFreezeNow(t *testing.T) {
	t.Run("collapses a ticking clock to one instant", func(t *testing.T) {
		base := time.Date(2026, 6, 8, 16, 16, 43, 0, time.UTC)
		ticks := 0
		ticking := func() time.Time {
			ticks++
			return base.Add(time.Duration(ticks) * time.Second)
		}

		frozen := freezeNow(ticking)
		first := frozen()
		for i := 0; i < 5; i++ {
			if got := frozen(); !got.Equal(first) {
				t.Fatalf("call %d returned %s, want frozen %s", i, got, first)
			}
		}
		// The underlying clock is read exactly once, at freeze time, no matter
		// how many mint sites consult the frozen clock afterward.
		if ticks != 1 {
			t.Fatalf("freezeNow read the underlying clock %d times, want 1", ticks)
		}
	})

	t.Run("idempotent: freezing a frozen clock keeps the same instant", func(t *testing.T) {
		base := time.Date(2026, 6, 8, 16, 16, 43, 0, time.UTC)
		ticking := func() time.Time {
			base = base.Add(time.Second)
			return base
		}

		once := freezeNow(ticking)
		want := once()
		twice := freezeNow(once)
		if got := twice(); !got.Equal(want) {
			t.Fatalf("re-freezing changed the instant: got %s, want %s", got, want)
		}
	})

	t.Run("nil clock falls back to time.Now", func(t *testing.T) {
		before := time.Now()
		got := freezeNow(nil)()
		after := time.Now()
		if got.Before(before) || got.After(after) {
			t.Fatalf("freezeNow(nil) = %s, want within [%s, %s]", got, before, after)
		}
	})
}
