package build

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared/diskheadroom"
)

type diskHeadroomRead struct {
	free        uint64
	total       uint64
	path        string
	sharedPaths []string
	ok          bool
}

func (r diskHeadroomRead) measurement() diskheadroom.DiskHeadroomMeasurement {
	return diskheadroom.DiskHeadroomMeasurement{Free: r.free, Total: r.total, Path: r.path, SharedPaths: r.sharedPaths}
}

type diskHeadroomCase struct {
	name          string
	policy        diskheadroom.DiskHeadroomPolicy
	dryRun        bool
	reads         []diskHeadroomRead
	reclaimable   uint64
	reclaimableOK bool
	// cacheReclaimable is read only once a prune has failed to close the gap.
	cacheReclaimable   uint64
	cacheReclaimableOK bool
	pruneErr           error
	wantErr            bool
	wantErrSubstr      string
	wantErrContains    []string
	wantErrExcludes    []string
	wantPrune          bool
}

const (
	diskHeadroomTestFloor uint64 = 20 << 30 // 20 GiB
	diskHeadroomBelow            = diskHeadroomTestFloor - (1 << 30)
	diskHeadroomAbove            = diskHeadroomTestFloor + (1 << 30)

	dockerRootPath          = "/var/lib/docker"
	dindDockerRootPath      = "/dind/docker"
	environmentWorkCloneDir = "/home/erun/work"
	environmentCacheDir     = "/home/erun/.cache"
)

func diskHeadroomCases() []diskHeadroomCase {
	return []diskHeadroomCase{
		{
			name:   "free space above floor: no prune",
			policy: diskheadroom.ReleaseDiskHeadroomPolicy,
			reads:  []diskHeadroomRead{{free: diskHeadroomAbove, ok: true}},
		},
		{
			name:        "free space below floor: prune runs, re-check above floor passes",
			policy:      diskheadroom.ReleaseDiskHeadroomPolicy,
			reads:       []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}, {free: diskHeadroomAbove, ok: true}},
			reclaimable: 1 << 40, reclaimableOK: true,
			wantPrune: true,
		},
		{
			// Refusing every build on a full node blocks the work that clears it.
			name:        "build still below floor after pruning warns but proceeds",
			policy:      buildDiskHeadroomPolicy,
			reads:       []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}, {free: diskHeadroomBelow, ok: true}},
			reclaimable: 1 << 40, reclaimableOK: true,
			wantPrune: true,
		},
		{
			name:        "a failed prune is non-fatal but the disk can still refuse afterward",
			policy:      diskheadroom.ReleaseDiskHeadroomPolicy,
			reads:       []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}, {free: diskHeadroomBelow, ok: true}},
			pruneErr:    errors.New("boom"),
			reclaimable: 1 << 40, reclaimableOK: true,
			wantPrune: true,
			wantErr:   true,
		},
		{
			// docker system df understates reclaimable cache; a nonzero figure is only a lower bound.
			name:        "reclaimable understated but non-zero: prunes instead of refusing on the stale figure",
			policy:      diskheadroom.ReleaseDiskHeadroomPolicy,
			reads:       []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}, {free: diskHeadroomAbove, ok: true}},
			reclaimable: 1 << 20, reclaimableOK: true,
			wantPrune: true,
		},
		{
			name:        "a build attempts a prune on a zero reclaimable figure and proceeds",
			policy:      buildDiskHeadroomPolicy,
			reads:       []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}, {free: diskHeadroomBelow, ok: true}},
			reclaimable: 0, reclaimableOK: true,
			wantPrune: true,
		},
		{
			name:      "an unknown reclaimable figure still prunes",
			policy:    diskheadroom.ReleaseDiskHeadroomPolicy,
			reads:     []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}, {free: diskHeadroomAbove, ok: true}},
			wantPrune: true,
		},
		{
			name:   "dry run: neither reads free space nor prunes",
			policy: diskheadroom.ReleaseDiskHeadroomPolicy,
			dryRun: true,
			// Never consumed: dry run returns before the first read.
			reads: []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}},
		},
	}
}

func diskHeadroomRemedyCases() []diskHeadroomCase {
	return []diskHeadroomCase{
		{
			name:   "a shortfall the build cache still holds names the cache, not image removal",
			policy: diskheadroom.ReleaseDiskHeadroomPolicy,
			reads: []diskHeadroomRead{
				{free: diskHeadroomBelow, path: dockerRootPath, ok: true},
				{free: diskHeadroomBelow, path: dockerRootPath, ok: true},
			},
			reclaimable:        28 << 30,
			reclaimableOK:      true,
			cacheReclaimable:   uint64(43.37 * 1e9),
			cacheReclaimableOK: true,
			wantPrune:          true,
			wantErr:            true,
			wantErrContains: []string{
				"build cache still holds",
				"docker buildx prune -a",
				dockerRootPath,
				"grow the volume",
			},
			wantErrExcludes: []string{"docker system prune", "remove unused images", "held outside docker"},
		},
	}
}

func TestEnsureDiskHeadroomWith(t *testing.T) {
	cases := append(diskHeadroomCases(), diskHeadroomRemedyCases()...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runDiskHeadroomCase(t, tc)
		})
	}
}

func runDiskHeadroomCase(t *testing.T, tc diskHeadroomCase) {
	t.Helper()
	t.Setenv(diskheadroom.ReleaseMinDiskHeadroomEnv, strconv.FormatUint(diskHeadroomTestFloor, 10))

	wantReads := 0
	if !tc.dryRun {
		wantReads = len(tc.reads)
	}

	readCalls := 0
	readFree := func(diskheadroom.DiskHeadroomTimeouts) (diskheadroom.DiskHeadroomMeasurement, error) {
		read := tc.reads[readCalls]
		readCalls++
		if !read.ok {
			return read.measurement(), errors.New("free disk space is unreadable")
		}
		return read.measurement(), nil
	}
	readReclaimable := func(time.Duration) (uint64, error) {
		if !tc.reclaimableOK {
			return 0, errors.New("docker system df is unreadable")
		}
		return tc.reclaimable, nil
	}
	readCacheReclaimable := func(time.Duration) (uint64, error) {
		if !tc.cacheReclaimableOK {
			return 0, errors.New("docker buildx du is unreadable")
		}
		return tc.cacheReclaimable, nil
	}
	pruneCalls := 0
	var prunedTo uint64
	prune := func(target uint64, _ time.Duration) error {
		pruneCalls++
		prunedTo = target
		return tc.pruneErr
	}

	err := diskheadroom.EnsureDiskHeadroomWith(core.Context{DryRun: tc.dryRun}, tc.policy, readFree, readReclaimable, readCacheReclaimable, prune)
	assertDiskHeadroomMessage(t, tc, err)

	if gotPrune := pruneCalls > 0; gotPrune != tc.wantPrune {
		t.Fatalf("prune called = %v, want %v (calls=%d)", gotPrune, tc.wantPrune, pruneCalls)
	}
	if tc.wantPrune && prunedTo != diskHeadroomTestFloor {
		t.Fatalf("expected the prune bounded to the floor (%d), got %d", diskHeadroomTestFloor, prunedTo)
	}
	if readCalls != wantReads {
		t.Fatalf("expected %d free-space reads, got %d", wantReads, readCalls)
	}
}

func assertDiskHeadroomMessage(t *testing.T, tc diskHeadroomCase, err error) {
	t.Helper()
	if tc.wantErr != (err != nil) {
		t.Fatalf("error = %v, wantErr = %v", err, tc.wantErr)
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	if tc.wantErrSubstr != "" && !strings.Contains(message, tc.wantErrSubstr) {
		t.Fatalf("expected error to contain %q, got %v", tc.wantErrSubstr, err)
	}
	for _, want := range tc.wantErrContains {
		if !strings.Contains(message, want) {
			t.Fatalf("expected the message to contain %q, got %q", want, message)
		}
	}
	for _, unwanted := range tc.wantErrExcludes {
		if strings.Contains(message, unwanted) {
			t.Fatalf("expected the message not to contain %q, got %q", unwanted, message)
		}
	}
}
