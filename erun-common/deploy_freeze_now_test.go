package eruncommon

import (
	"testing"
	"time"
)

// TestFreezeNow pins the invariant that a whole multi-image deploy is stamped
// with a single snapshot timestamp: freezeNow reads the clock once so every
// independent version-mint site observes the same instant. Otherwise the stamps
// drift and the runtime chart can persist a phantom RuntimeVersion that was
// never built or pushed — one the deploy picker can never offer because it gates
// on registry presence.
//
// The integration dry-run cannot catch this (its real clock lands every mint in
// the same wall-clock second, and the golden normalizer collapses versions), so
// this white-box test owns the contract.
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
