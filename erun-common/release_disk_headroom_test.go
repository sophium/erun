package eruncommon

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestParseDFAvailableBytes(t *testing.T) {
	t.Run("standard one-line output", func(t *testing.T) {
		output := "Filesystem     1024-blocks     Used Available Capacity Mounted on\n" +
			"/dev/sda1        102400000 51200000  41943040      56% /var/lib/docker\n"
		got, ok := parseDFAvailableBytes(output)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if want := uint64(41943040) * 1024; got != want {
			t.Fatalf("got %d, want %d", got, want)
		}
	})

	t.Run("long filesystem name wraps onto its own line", func(t *testing.T) {
		output := "Filesystem                                                          1024-blocks     Used Available Capacity Mounted on\n" +
			"a-very-long-overlay-filesystem-identifier-that-wraps-the-data-row\n" +
			"                                                                       102400000 51200000  20971520      51% /var/lib/docker\n"
		got, ok := parseDFAvailableBytes(output)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if want := uint64(20971520) * 1024; got != want {
			t.Fatalf("got %d, want %d", got, want)
		}
	})

	t.Run("empty output is inconclusive", func(t *testing.T) {
		if _, ok := parseDFAvailableBytes(""); ok {
			t.Fatal("expected ok=false for empty output")
		}
	})

	t.Run("malformed data row is inconclusive", func(t *testing.T) {
		output := "Filesystem     1024-blocks     Used Available Capacity Mounted on\nnot enough fields\n"
		if _, ok := parseDFAvailableBytes(output); ok {
			t.Fatal("expected ok=false for a data row with too few fields")
		}
	})
}

func TestResolveReleaseMinDiskHeadroomBytes(t *testing.T) {
	t.Run("defaults when unset", func(t *testing.T) {
		t.Setenv(releaseMinDiskHeadroomEnv, "")
		if got := resolveReleaseMinDiskHeadroomBytes(); got != releaseMinDiskHeadroomBytes {
			t.Fatalf("got %d, want default %d", got, releaseMinDiskHeadroomBytes)
		}
	})

	t.Run("honors a valid override", func(t *testing.T) {
		t.Setenv(releaseMinDiskHeadroomEnv, "1073741824")
		if got := resolveReleaseMinDiskHeadroomBytes(); got != 1073741824 {
			t.Fatalf("got %d, want 1073741824", got)
		}
	})

	t.Run("falls back to the default on a malformed override", func(t *testing.T) {
		t.Setenv(releaseMinDiskHeadroomEnv, "not-a-number")
		if got := resolveReleaseMinDiskHeadroomBytes(); got != releaseMinDiskHeadroomBytes {
			t.Fatalf("got %d, want default %d", got, releaseMinDiskHeadroomBytes)
		}
	})
}

func TestFormatGiB(t *testing.T) {
	if got, want := formatGiB(20<<30), "20.0 GiB"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestEnsureReleaseDiskHeadroomWith drives the decision logic with injected
// fakes instead of a real docker daemon, per erun-common/AGENTS.md's
// dependency-injection-over-globals guidance (mirroring the existing
// GitCommandRunnerFunc-injection shape ensureReleaseBaseBranchUnmoved uses).
func TestEnsureReleaseDiskHeadroomWith(t *testing.T) {
	const testFloor uint64 = 20 << 30 // 20 GiB

	setFloor := func(t *testing.T) {
		t.Helper()
		t.Setenv(releaseMinDiskHeadroomEnv, strconv.FormatUint(testFloor, 10))
	}

	t.Run("free space above floor: no prune", func(t *testing.T) {
		setFloor(t)
		pruneCalls := 0
		readFree := func() (uint64, bool) { return testFloor + (1 << 30), true }
		prune := func(uint64) error { pruneCalls++; return nil }

		if err := ensureReleaseDiskHeadroomWith(Context{}, readFree, prune); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pruneCalls != 0 {
			t.Fatalf("expected no prune when free space is already above the floor, got %d calls", pruneCalls)
		}
	})

	t.Run("free space below floor: prune runs, re-check above floor passes", func(t *testing.T) {
		setFloor(t)
		reads := 0
		readFree := func() (uint64, bool) {
			reads++
			if reads == 1 {
				return testFloor - (1 << 30), true // below floor
			}
			return testFloor + (1 << 30), true // pruning freed enough
		}
		pruneCalls := 0
		var prunedTo uint64
		prune := func(target uint64) error {
			pruneCalls++
			prunedTo = target
			return nil
		}

		if err := ensureReleaseDiskHeadroomWith(Context{}, readFree, prune); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pruneCalls != 1 {
			t.Fatalf("expected exactly one prune, got %d", pruneCalls)
		}
		if prunedTo != testFloor {
			t.Fatalf("expected the prune bounded to the floor (%d), got %d", testFloor, prunedTo)
		}
		if reads != 2 {
			t.Fatalf("expected a re-check read after pruning, got %d reads", reads)
		}
	})

	t.Run("free space below floor: still below floor after pruning refuses", func(t *testing.T) {
		setFloor(t)
		readFree := func() (uint64, bool) { return testFloor - (1 << 30), true }
		pruneCalls := 0
		prune := func(uint64) error { pruneCalls++; return nil }

		err := ensureReleaseDiskHeadroomWith(Context{}, readFree, prune)
		if err == nil {
			t.Fatal("expected a refusal when the disk is still below the floor after pruning")
		}
		if pruneCalls != 1 {
			t.Fatalf("expected exactly one prune attempt, got %d", pruneCalls)
		}
		if !strings.Contains(err.Error(), "filling this disk is what evicts the pod running the release") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("a failed prune is non-fatal but the disk can still refuse afterward", func(t *testing.T) {
		setFloor(t)
		readFree := func() (uint64, bool) { return testFloor - (1 << 30), true }
		prune := func(uint64) error { return errors.New("boom") }

		if err := ensureReleaseDiskHeadroomWith(Context{}, readFree, prune); err == nil {
			t.Fatal("expected a refusal even though the failing prune itself did not return the error")
		}
	})

	t.Run("inconclusive read: skip the check, no prune, no refusal", func(t *testing.T) {
		setFloor(t)
		readFree := func() (uint64, bool) { return 0, false }
		pruneCalls := 0
		prune := func(uint64) error { pruneCalls++; return nil }

		if err := ensureReleaseDiskHeadroomWith(Context{}, readFree, prune); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pruneCalls != 0 {
			t.Fatalf("expected no prune on an inconclusive read, got %d calls", pruneCalls)
		}
	})

	t.Run("dry run: neither reads free space nor prunes", func(t *testing.T) {
		setFloor(t)
		readCalls, pruneCalls := 0, 0
		readFree := func() (uint64, bool) { readCalls++; return testFloor - (1 << 30), true }
		prune := func(uint64) error { pruneCalls++; return nil }

		if err := ensureReleaseDiskHeadroomWith(Context{DryRun: true}, readFree, prune); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if readCalls != 0 || pruneCalls != 0 {
			t.Fatalf("expected no reads or prunes in dry run, got reads=%d prunes=%d", readCalls, pruneCalls)
		}
	})
}
