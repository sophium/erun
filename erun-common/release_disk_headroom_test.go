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

// diskHeadroomCase drives one pass of the decision logic. The floor is pinned
// by env in the test body, so free-space figures here are relative to it.
type diskHeadroomCase struct {
	name          string
	policy        diskHeadroomPolicy
	dryRun        bool
	reads         []diskHeadroomRead
	reclaimable   uint64
	reclaimableOK bool
	pruneErr      error
	wantErr       bool
	wantErrSubstr string
	wantPrune     bool
}

const (
	diskHeadroomTestFloor uint64 = 20 << 30 // 20 GiB
	diskHeadroomBelow            = diskHeadroomTestFloor - (1 << 30)
	diskHeadroomAbove            = diskHeadroomTestFloor + (1 << 30)
)

func diskHeadroomCases() []diskHeadroomCase {
	return []diskHeadroomCase{
		{
			name:   "free space above floor: no prune",
			policy: releaseDiskHeadroomPolicy,
			reads:  []diskHeadroomRead{{free: diskHeadroomAbove, ok: true}},
		},
		{
			name:        "free space below floor: prune runs, re-check above floor passes",
			policy:      releaseDiskHeadroomPolicy,
			reads:       []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}, {free: diskHeadroomAbove, ok: true}},
			reclaimable: 1 << 40, reclaimableOK: true,
			wantPrune: true,
		},
		{
			name:          "release still below floor after pruning refuses",
			policy:        releaseDiskHeadroomPolicy,
			reads:         []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}, {free: diskHeadroomBelow, ok: true}},
			wantPrune:     true,
			wantErr:       true,
			wantErrSubstr: "filling this disk is what evicts the pod running the release",
		},
		{
			// A build is small enough that proceeding is usually fine, and
			// refusing every build on a full node blocks the work that clears it.
			name:        "build still below floor after pruning warns but proceeds",
			policy:      buildDiskHeadroomPolicy,
			reads:       []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}, {free: diskHeadroomBelow, ok: true}},
			reclaimable: 1 << 40, reclaimableOK: true,
			wantPrune: true,
		},
		{
			name:        "a failed prune is non-fatal but the disk can still refuse afterward",
			policy:      releaseDiskHeadroomPolicy,
			reads:       []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}, {free: diskHeadroomBelow, ok: true}},
			pruneErr:    errors.New("boom"),
			reclaimable: 1 << 40, reclaimableOK: true,
			wantPrune: true,
			wantErr:   true,
		},
		{
			// docker system df understated real reclaimable build cache by 4.4x
			// on a real node (erun#2431): a small-but-nonzero reported figure must
			// not be read as "the prune can't help" — it is only a lower bound, so
			// the prune still runs, and here it turns out to close the gap.
			name:        "reclaimable understated but non-zero: prunes instead of refusing on the stale figure",
			policy:      releaseDiskHeadroomPolicy,
			reads:       []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}, {free: diskHeadroomAbove, ok: true}},
			reclaimable: 1 << 20, reclaimableOK: true,
			wantPrune: true,
		},
		{
			// Zero, unlike a small positive figure, is trusted: there is
			// genuinely nothing a build-cache prune could do, so skip it rather
			// than run a real no-op.
			name:          "reclaimable genuinely zero: declined, not attempted",
			policy:        releaseDiskHeadroomPolicy,
			reads:         []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}},
			reclaimable:   0,
			reclaimableOK: true,
			wantPrune:     false,
			wantErr:       true,
			wantErrSubstr: "filling this disk is what evicts the pod running the release",
		},
		{
			// A build declines the same no-op prune but still proceeds.
			name:          "a build declines a genuinely empty prune and proceeds",
			policy:        buildDiskHeadroomPolicy,
			reads:         []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}},
			reclaimable:   0,
			reclaimableOK: true,
			wantPrune:     false,
		},
		{
			// An unreadable figure is not a reason to skip the remedy.
			name:      "an unknown reclaimable figure still prunes",
			policy:    releaseDiskHeadroomPolicy,
			reads:     []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}, {free: diskHeadroomAbove, ok: true}},
			wantPrune: true,
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
			reads: []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}},
		},
	}
}

// TestEnsureDiskHeadroomWith drives the decision logic with injected
// fakes instead of a real docker daemon, per erun-common/AGENTS.md's
// dependency-injection-over-globals guidance (mirroring the existing
// GitCommandRunnerFunc-injection shape ensureReleaseBaseBranchUnmoved uses).
func TestEnsureDiskHeadroomWith(t *testing.T) {
	for _, tc := range diskHeadroomCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(releaseMinDiskHeadroomEnv, strconv.FormatUint(diskHeadroomTestFloor, 10))

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
			readReclaimable := func() (uint64, bool) {
				return tc.reclaimable, tc.reclaimableOK
			}
			pruneCalls := 0
			var prunedTo uint64
			prune := func(target uint64) error {
				pruneCalls++
				prunedTo = target
				return tc.pruneErr
			}

			err := ensureDiskHeadroomWith(Context{DryRun: tc.dryRun}, tc.policy, readFree, readReclaimable, prune)

			if tc.wantErr != (err != nil) {
				t.Fatalf("error = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantErrSubstr != "" && !strings.Contains(err.Error(), tc.wantErrSubstr) {
				t.Fatalf("expected error to contain %q, got %v", tc.wantErrSubstr, err)
			}
			if gotPrune := pruneCalls > 0; gotPrune != tc.wantPrune {
				t.Fatalf("prune called = %v, want %v (calls=%d)", gotPrune, tc.wantPrune, pruneCalls)
			}
			if tc.wantPrune && prunedTo != diskHeadroomTestFloor {
				t.Fatalf("expected the prune bounded to the floor (%d), got %d", diskHeadroomTestFloor, prunedTo)
			}
			if readCalls != wantReads {
				t.Fatalf("expected %d free-space reads, got %d", wantReads, readCalls)
			}
		})
	}
}

func TestParseDockerSize(t *testing.T) {
	// Shapes taken from real `docker system df --format` output on a build box.
	cases := []struct {
		in   string
		want uint64
		ok   bool
	}{
		{"8.914GB", 8914000000, true},
		{"0B", 0, true},
		{"250.8MB", 250800000, true},
		{"103.4GB", 103400000000, true},
		{"512kB", 512000, true},
		// Some rows carry a trailing percentage; the number is still the size.
		{"77.29GB (100%)", 77290000000, true},
		{"", 0, false},
		{"not-a-size", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseDockerSize(tc.in)
		if ok != tc.ok {
			t.Errorf("parseDockerSize(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("parseDockerSize(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
