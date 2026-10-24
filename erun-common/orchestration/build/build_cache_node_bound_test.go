package build

import (
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared/diskheadroom"
)

// Literal names rather than constants: if the rendered variable moves, these
// cases fail instead of passing on a bound that no longer arrives.
const (
	nodeBytesVar = "ERUN_BUILD_CACHE_NODE_BYTES"
	coTenantsVar = "ERUN_BUILD_CACHE_CO_TENANTS"
)

type nodeBoundCase struct {
	name      string
	nodeGi    uint64
	coTenants uint64
	// volumeGi is the chart's docker volume, still the ceiling when tighter.
	volumeGi uint64
	cacheGi  uint64

	// wantPrunedTo zero means no reclaim is expected.
	wantPrunedTo uint64

	// wantNodeShareUnusable means the node share is too small to serve a build, so
	// the volume share holds and the run must trace that instead.
	wantNodeShareUnusable bool
}

func nodeBoundCases() []nodeBoundCase {
	return append(append(nodeBoundAggregateCases(), nodeBoundUnusableShareCases()...), nodeBoundControlCases()...)
}

func nodeBoundAggregateCases() []nodeBoundCase {
	return []nodeBoundCase{
		{
			// Four 40 GiB volume ceilings total 160 GiB on a node with 100 GiB above its reserve.
			name:         "four environments each inside their own volume ceiling still exceed the node",
			nodeGi:       120,
			coTenants:    4,
			volumeGi:     50,
			cacheGi:      45,
			wantPrunedTo: 20 << 30,
		},
		{
			name:         "a node share larger than the volume keeps the volume ceiling",
			nodeGi:       400,
			coTenants:    4,
			volumeGi:     50,
			cacheGi:      45,
			wantPrunedTo: 40 << 30,
		},
		{
			name:         "the ceiling follows the node and the cache count together",
			nodeGi:       170,
			coTenants:    6,
			volumeGi:     50,
			cacheGi:      45,
			wantPrunedTo: 20 << 30,
		},
		{
			// A missing count is not a count of one, which would give one env the whole node.
			name:         "a node size with no cache count does not bound anything",
			nodeGi:       100,
			coTenants:    0,
			volumeGi:     50,
			cacheGi:      45,
			wantPrunedTo: 40 << 30,
		},
		{
			name:         "an environment that declares no node keeps its volume ceiling",
			nodeGi:       0,
			coTenants:    0,
			volumeGi:     50,
			cacheGi:      45,
			wantPrunedTo: 40 << 30,
		},
		{
			// A 20 GiB floor exceeds a 16 GiB node; zero would mean `--max-used-space 0`
			// on every build.
			name:         "a node at or below its own disk floor does not bound the cache to nothing",
			nodeGi:       16,
			coTenants:    4,
			volumeGi:     50,
			cacheGi:      45,
			wantPrunedTo: 40 << 30,
		},
		{
			name:         "a node exactly at its disk floor does not bound the cache to nothing",
			nodeGi:       20,
			coTenants:    4,
			volumeGi:     50,
			cacheGi:      45,
			wantPrunedTo: 40 << 30,
		},
	}
}

// nodeBoundUnusableShareCases leave a share too small for any build to be served from.
func nodeBoundUnusableShareCases() []nodeBoundCase {
	return []nodeBoundCase{
		{
			// 21 GiB is the smallest declarable node above the floor; four ways gives a
			// ~0.2 GB ceiling, effectively zero.
			name:                  "a node one GiB above its floor does not install a sub-gigabyte ceiling",
			nodeGi:                21,
			coTenants:             4,
			volumeGi:              50,
			cacheGi:               45,
			wantPrunedTo:          40 << 30,
			wantNodeShareUnusable: true,
		},
		{
			// Under formatGiB's rounding this ceiling would trace as "0.0 GiB allowed".
			name:                  "a node one GiB above its floor divided sixteen ways does not install a fifty-megabyte ceiling",
			nodeGi:                21,
			coTenants:             16,
			volumeGi:              50,
			cacheGi:               45,
			wantPrunedTo:          40 << 30,
			wantNodeShareUnusable: true,
		},
		{
			name:                  "a node one GiB above its floor divided a million ways does not install a byte-scale ceiling",
			nodeGi:                21,
			coTenants:             1000000,
			volumeGi:              50,
			cacheGi:               45,
			wantPrunedTo:          40 << 30,
			wantNodeShareUnusable: true,
		},
		{
			name:                  "a thirty-two GiB node divided sixty-four ways does not install a hundred-megabyte ceiling",
			nodeGi:                32,
			coTenants:             64,
			volumeGi:              50,
			cacheGi:               45,
			wantPrunedTo:          40 << 30,
			wantNodeShareUnusable: true,
		},
	}
}

func nodeBoundControlCases() []nodeBoundCase {
	return []nodeBoundCase{
		{
			// Guards against satisfying the cases above by disabling the node bound.
			name:         "a node whose share still holds a working set tightens the ceiling",
			nodeGi:       60,
			coTenants:    4,
			volumeGi:     50,
			cacheGi:      45,
			wantPrunedTo: 8589934560,
		},
		{
			// The usability floor must not scale with node size.
			name:         "a large node divided many ways still tightens the ceiling",
			nodeGi:       400,
			coTenants:    8,
			volumeGi:     50,
			cacheGi:      45,
			wantPrunedTo: 38654705600,
		},
		{
			name:         "a cache under the node-derived mark is left alone",
			nodeGi:       120,
			coTenants:    4,
			volumeGi:     50,
			cacheGi:      10,
			wantPrunedTo: 0,
		},
	}
}

// The per-environment ceilings must sum to no more than the node has above its reserve.
func TestBuildCacheCeilingsStayBoundedAcrossTheNodesEnvironments(t *testing.T) {
	for _, tc := range nodeBoundCases() {
		t.Run(tc.name, func(t *testing.T) {
			runNodeBoundCase(t, tc)
		})
	}
}

// resolveBuildCacheBounds divides before multiplying, so a byte-scale share
// would round to a zero ceiling.
func TestBuildCacheRetentionDoesNotBoundToANodeShareOfNoBytes(t *testing.T) {
	const (
		floorBytes  = 100
		nodeBytes   = 250
		coTenants   = 4
		volumeGi    = 50
		volumeBytes = volumeGi << 30
		cacheBytes  = 45 << 30
	)
	t.Setenv(diskheadroom.ReleaseMinDiskHeadroomEnv, strconv.FormatUint(floorBytes, 10))
	t.Setenv(nodeBytesVar, strconv.FormatUint(nodeBytes, 10))
	t.Setenv(coTenantsVar, strconv.FormatUint(coTenants, 10))

	var prunedTo uint64
	logs := &strings.Builder{}
	diskheadroom.EnsureBuildCacheRetentionWith(core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, logs, logs)}, buildDiskHeadroomPolicy,
		func() (uint64, error) { return volumeBytes, nil },
		func(time.Duration) (uint64, error) { return cacheBytes, nil },
		func(ceiling uint64, _ time.Duration) error { prunedTo = ceiling; return nil })

	want := diskheadroom.ResolveBuildCacheBounds(volumeBytes).Ceiling
	if prunedTo != want {
		t.Fatalf("a node share with a ceiling of no bytes was reclaimed to %s, want the volume ceiling %s; output was %q",
			diskheadroom.FormatGiB(prunedTo), diskheadroom.FormatGiB(want), logs.String())
	}
}

// nodeReserveGi uses the disk-headroom preflight's own floor.
func nodeReserveGi(nodeGi uint64) uint64 {
	return diskheadroom.ResolveMinDiskHeadroomBytes(nodeGi<<30) >> 30
}

func runNodeBoundCase(t *testing.T, tc nodeBoundCase) {
	t.Helper()

	// Clear the floor override so the machine's environment cannot leak in.
	t.Setenv(diskheadroom.ReleaseMinDiskHeadroomEnv, "")
	t.Setenv(nodeBytesVar, "")
	t.Setenv(coTenantsVar, "")
	if tc.nodeGi > 0 {
		t.Setenv(nodeBytesVar, strconv.FormatUint(tc.nodeGi<<30, 10))
	}
	if tc.coTenants > 0 {
		t.Setenv(coTenantsVar, strconv.FormatUint(tc.coTenants, 10))
	}

	var prunedTo uint64
	prune := func(ceiling uint64, _ time.Duration) error {
		prunedTo = ceiling
		return nil
	}

	logs := &strings.Builder{}
	// Capture traces: a rejected node budget is reported only as a trace.
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, io.Discard, io.Discard).WithTraceSink(logs)}
	diskheadroom.EnsureBuildCacheRetentionWith(ctx, buildDiskHeadroomPolicy,
		func() (uint64, error) { return tc.volumeGi << 30, nil },
		func(time.Duration) (uint64, error) { return tc.cacheGi << 30, nil },
		prune)

	if prunedTo != tc.wantPrunedTo {
		t.Fatalf("the cache was reclaimed to %s, want the ceiling %s; output was %q",
			diskheadroom.FormatGiB(prunedTo), diskheadroom.FormatGiB(tc.wantPrunedTo), logs.String())
	}
	if tc.wantPrunedTo == 0 {
		return
	}

	// A refused share must be disclosed and must not invent a ceiling.
	if tc.wantNodeShareUnusable {
		if !strings.Contains(logs.String(), "nothing on this run bounds the sum across the node's environments") {
			t.Fatalf("a node budget that yields no usable share was passed over without saying so; output was %q", logs.String())
		}
		return
	}

	// A node at or below its reserve has no share in force, so no sum to check.
	if tc.coTenants == 0 || tc.nodeGi <= nodeReserveGi(tc.nodeGi) {
		return
	}
	reserve := nodeReserveGi(tc.nodeGi)
	budget := (tc.nodeGi - reserve) << 30
	if spent := prunedTo * tc.coTenants; spent > budget {
		t.Fatalf("the %d environments' ceilings sum to %s, past the %s a %d GiB node has above its %d GiB reserve: each environment is inside its own bound and the node fills anyway",
			tc.coTenants, diskheadroom.FormatGiB(spent), diskheadroom.FormatGiB(budget), tc.nodeGi, reserve)
	}
}
