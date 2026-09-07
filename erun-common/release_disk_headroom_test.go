package eruncommon

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestParseDFDiskBytes(t *testing.T) {
	t.Run("standard one-line output", func(t *testing.T) {
		output := "Filesystem     1024-blocks     Used Available Capacity Mounted on\n" +
			"/dev/sda1        102400000 51200000  41943040      56% /var/lib/docker\n"
		free, total, ok := parseDFDiskBytes(output)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if want := uint64(41943040) * 1024; free != want {
			t.Fatalf("free = %d, want %d", free, want)
		}
		if want := uint64(102400000) * 1024; total != want {
			t.Fatalf("total = %d, want %d", total, want)
		}
	})

	t.Run("long filesystem name wraps onto its own line", func(t *testing.T) {
		output := "Filesystem                                                          1024-blocks     Used Available Capacity Mounted on\n" +
			"a-very-long-overlay-filesystem-identifier-that-wraps-the-data-row\n" +
			"                                                                       102400000 51200000  20971520      51% /var/lib/docker\n"
		free, total, ok := parseDFDiskBytes(output)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if want := uint64(20971520) * 1024; free != want {
			t.Fatalf("free = %d, want %d", free, want)
		}
		// The wrapped row drops the filesystem field, shifting every column
		// one left — the total still has to be found relative to Capacity.
		if want := uint64(102400000) * 1024; total != want {
			t.Fatalf("total = %d, want %d", total, want)
		}
	})

	t.Run("empty output is inconclusive", func(t *testing.T) {
		if _, _, ok := parseDFDiskBytes(""); ok {
			t.Fatal("expected ok=false for empty output")
		}
	})

	t.Run("malformed data row is inconclusive", func(t *testing.T) {
		output := "Filesystem     1024-blocks     Used Available Capacity Mounted on\nnot enough fields\n"
		if _, _, ok := parseDFDiskBytes(output); ok {
			t.Fatal("expected ok=false for a data row with too few fields")
		}
	})
}

func TestResolveMinDiskHeadroomBytes(t *testing.T) {
	// A filesystem small enough that 10% of it is under the absolute floor.
	const smallDisk uint64 = 100 << 30 // 100 GiB -> 10 GiB proportional
	// A filesystem big enough that 10% of it clears the absolute floor — the
	// shape of the node that evicted every pod while free space still sat
	// above a 20 GiB floor.
	const largeDisk uint64 = 435 << 30 // 435 GiB -> 43.5 GiB proportional

	t.Run("defaults to the absolute floor when total is unknown", func(t *testing.T) {
		t.Setenv(releaseMinDiskHeadroomEnv, "")
		if got := resolveMinDiskHeadroomBytes(0); got != releaseMinDiskHeadroomBytes {
			t.Fatalf("got %d, want default %d", got, releaseMinDiskHeadroomBytes)
		}
	})

	t.Run("keeps the absolute floor when it exceeds the proportional one", func(t *testing.T) {
		t.Setenv(releaseMinDiskHeadroomEnv, "")
		if got := resolveMinDiskHeadroomBytes(smallDisk); got != releaseMinDiskHeadroomBytes {
			t.Fatalf("got %d, want the absolute floor %d", got, releaseMinDiskHeadroomBytes)
		}
	})

	t.Run("scales past the absolute floor on a large disk", func(t *testing.T) {
		t.Setenv(releaseMinDiskHeadroomEnv, "")
		want := largeDisk / 100 * minDiskHeadroomPercent
		got := resolveMinDiskHeadroomBytes(largeDisk)
		if got != want {
			t.Fatalf("got %d, want proportional %d", got, want)
		}
		if got <= releaseMinDiskHeadroomBytes {
			t.Fatalf("proportional floor %d should exceed the absolute floor %d", got, releaseMinDiskHeadroomBytes)
		}
	})

	// The whole point of the proportional floor: kubelet's evictionHard for
	// nodefs defaults to 5% available, so a floor at or below that line can
	// only prune after eviction has already begun.
	t.Run("clears kubelet's 5 percent eviction threshold", func(t *testing.T) {
		t.Setenv(releaseMinDiskHeadroomEnv, "")
		evictionThreshold := largeDisk / 100 * 5
		if got := resolveMinDiskHeadroomBytes(largeDisk); got <= evictionThreshold {
			t.Fatalf("floor %d must sit above the eviction threshold %d", got, evictionThreshold)
		}
	})

	t.Run("honors a valid override outright", func(t *testing.T) {
		t.Setenv(releaseMinDiskHeadroomEnv, "1073741824")
		if got := resolveMinDiskHeadroomBytes(largeDisk); got != 1073741824 {
			t.Fatalf("got %d, want 1073741824", got)
		}
	})

	t.Run("falls back to the resolved floor on a malformed override", func(t *testing.T) {
		t.Setenv(releaseMinDiskHeadroomEnv, "not-a-number")
		if got := resolveMinDiskHeadroomBytes(0); got != releaseMinDiskHeadroomBytes {
			t.Fatalf("got %d, want default %d", got, releaseMinDiskHeadroomBytes)
		}
	})
}

func TestFormatGiB(t *testing.T) {
	if got, want := formatGiB(20<<30), "20.0 GiB"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// diskHeadroomRead is one scripted response from a test's fake
// diskHeadroomFreeSpaceFunc.
type diskHeadroomRead struct {
	free  uint64
	total uint64
	ok    bool
}

// TestEnsureDiskHeadroomWith drives the decision logic with injected
// fakes instead of a real docker daemon, per erun-common/AGENTS.md's
// dependency-injection-over-globals guidance (mirroring the existing
// GitCommandRunnerFunc-injection shape ensureReleaseBaseBranchUnmoved uses).
func TestEnsureDiskHeadroomWith(t *testing.T) {
	const testFloor uint64 = 20 << 30 // 20 GiB
	const belowFloor = testFloor - (1 << 30)
	const aboveFloor = testFloor + (1 << 30)

	cases := []struct {
		name          string
		policy        diskHeadroomPolicy
		dryRun        bool
		reads         []diskHeadroomRead
		pruneErr      error
		wantErr       bool
		wantErrSubstr string
		wantPrune     bool
	}{
		{
			name:   "free space above floor: no prune",
			policy: releaseDiskHeadroomPolicy,
			reads:  []diskHeadroomRead{{free: aboveFloor, ok: true}},
		},
		{
			name:      "free space below floor: prune runs, re-check above floor passes",
			policy:    releaseDiskHeadroomPolicy,
			reads:     []diskHeadroomRead{{free: belowFloor, ok: true}, {free: aboveFloor, ok: true}},
			wantPrune: true,
		},
		{
			name:          "release still below floor after pruning refuses",
			policy:        releaseDiskHeadroomPolicy,
			reads:         []diskHeadroomRead{{free: belowFloor, ok: true}, {free: belowFloor, ok: true}},
			wantPrune:     true,
			wantErr:       true,
			wantErrSubstr: "filling this disk is what evicts the pod running the release",
		},
		{
			// A build is small enough that proceeding is usually fine, and
			// refusing every build on a full node blocks the work that clears it.
			name:      "build still below floor after pruning warns but proceeds",
			policy:    buildDiskHeadroomPolicy,
			reads:     []diskHeadroomRead{{free: belowFloor, ok: true}, {free: belowFloor, ok: true}},
			wantPrune: true,
			wantErr:   false,
		},
		{
			name:      "a failed prune is non-fatal but the disk can still refuse afterward",
			policy:    releaseDiskHeadroomPolicy,
			reads:     []diskHeadroomRead{{free: belowFloor, ok: true}, {free: belowFloor, ok: true}},
			pruneErr:  errors.New("boom"),
			wantPrune: true,
			wantErr:   true,
		},
		{
			name:   "inconclusive read: skip the check, no prune, no refusal",
			policy: releaseDiskHeadroomPolicy,
			reads:  []diskHeadroomRead{{ok: false}},
		},
		{
			name:   "dry run: neither reads free space nor prunes",
			policy: releaseDiskHeadroomPolicy,
			dryRun: true,
			// Never consumed: dry run must return before the first read.
			reads: []diskHeadroomRead{{free: belowFloor, ok: true}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(releaseMinDiskHeadroomEnv, strconv.FormatUint(testFloor, 10))

			wantReads := 0
			if !tc.dryRun {
				wantReads = len(tc.reads)
			}

			readCalls := 0
			readFree := func() (uint64, uint64, bool) {
				read := tc.reads[readCalls]
				readCalls++
				return read.free, read.total, read.ok
			}
			pruneCalls := 0
			var prunedTo uint64
			prune := func(target uint64) error {
				pruneCalls++
				prunedTo = target
				return tc.pruneErr
			}

			err := ensureDiskHeadroomWith(Context{DryRun: tc.dryRun}, tc.policy, readFree, prune)

			if tc.wantErr != (err != nil) {
				t.Fatalf("error = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantErrSubstr != "" && !strings.Contains(err.Error(), tc.wantErrSubstr) {
				t.Fatalf("expected error to contain %q, got %v", tc.wantErrSubstr, err)
			}
			if gotPrune := pruneCalls > 0; gotPrune != tc.wantPrune {
				t.Fatalf("prune called = %v, want %v (calls=%d)", gotPrune, tc.wantPrune, pruneCalls)
			}
			if tc.wantPrune && prunedTo != testFloor {
				t.Fatalf("expected the prune bounded to the floor (%d), got %d", testFloor, prunedTo)
			}
			if readCalls != wantReads {
				t.Fatalf("expected %d free-space reads, got %d", wantReads, readCalls)
			}
		})
	}
}
