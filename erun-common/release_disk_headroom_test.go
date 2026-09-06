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

// diskHeadroomRead is one scripted response from a test's fake
// diskHeadroomFreeSpaceFunc.
type diskHeadroomRead struct {
	free uint64
	ok   bool
}

// TestEnsureReleaseDiskHeadroomWith drives the decision logic with injected
// fakes instead of a real docker daemon, per erun-common/AGENTS.md's
// dependency-injection-over-globals guidance (mirroring the existing
// GitCommandRunnerFunc-injection shape ensureReleaseBaseBranchUnmoved uses).
func TestEnsureReleaseDiskHeadroomWith(t *testing.T) {
	const testFloor uint64 = 20 << 30 // 20 GiB
	const belowFloor = testFloor - (1 << 30)
	const aboveFloor = testFloor + (1 << 30)

	cases := []struct {
		name          string
		dryRun        bool
		reads         []diskHeadroomRead
		pruneErr      error
		wantErr       bool
		wantErrSubstr string
		wantPrune     bool
	}{
		{
			name:  "free space above floor: no prune",
			reads: []diskHeadroomRead{{aboveFloor, true}},
		},
		{
			name:      "free space below floor: prune runs, re-check above floor passes",
			reads:     []diskHeadroomRead{{belowFloor, true}, {aboveFloor, true}},
			wantPrune: true,
		},
		{
			name:          "free space below floor: still below floor after pruning refuses",
			reads:         []diskHeadroomRead{{belowFloor, true}, {belowFloor, true}},
			wantPrune:     true,
			wantErr:       true,
			wantErrSubstr: "filling this disk is what evicts the pod running the release",
		},
		{
			name:      "a failed prune is non-fatal but the disk can still refuse afterward",
			reads:     []diskHeadroomRead{{belowFloor, true}, {belowFloor, true}},
			pruneErr:  errors.New("boom"),
			wantPrune: true,
			wantErr:   true,
		},
		{
			name:  "inconclusive read: skip the check, no prune, no refusal",
			reads: []diskHeadroomRead{{0, false}},
		},
		{
			name:   "dry run: neither reads free space nor prunes",
			dryRun: true,
			// Never consumed: dry run must return before the first read.
			reads: []diskHeadroomRead{{belowFloor, true}},
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
			readFree := func() (uint64, bool) {
				read := tc.reads[readCalls]
				readCalls++
				return read.free, read.ok
			}
			pruneCalls := 0
			var prunedTo uint64
			prune := func(target uint64) error {
				pruneCalls++
				prunedTo = target
				return tc.pruneErr
			}

			err := ensureReleaseDiskHeadroomWith(Context{DryRun: tc.dryRun}, readFree, prune)

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
