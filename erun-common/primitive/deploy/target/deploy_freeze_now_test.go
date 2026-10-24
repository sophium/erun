package target

import (
	"testing"
	"time"
)

// TestFreezeNow pins that one deploy stamps every image with a single timestamp;
// drifting stamps can persist a RuntimeVersion that was never pushed.
func TestFreezeNow(t *testing.T) {
	t.Run("collapses a ticking clock to one instant", func(t *testing.T) {
		base := time.Date(2026, 6, 8, 16, 16, 43, 0, time.UTC)
		ticks := 0
		ticking := func() time.Time {
			ticks++
			return base.Add(time.Duration(ticks) * time.Second)
		}

		frozen := FreezeNow(ticking)
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

		once := FreezeNow(ticking)
		want := once()
		twice := FreezeNow(once)
		if got := twice(); !got.Equal(want) {
			t.Fatalf("re-freezing changed the instant: got %s, want %s", got, want)
		}
	})

	t.Run("nil clock falls back to time.Now", func(t *testing.T) {
		before := time.Now()
		got := FreezeNow(nil)()
		after := time.Now()
		if got.Before(before) || got.After(after) {
			t.Fatalf("freezeNow(nil) = %s, want within [%s, %s]", got, before, after)
		}
	})
}
