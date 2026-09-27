package eruncommon

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// The two variables the deployment renders to describe the node this
// environment's build cache shares. They are spelled out here rather than
// referred to by their constants on purpose: this file is the reproduction of a
// defect that predates those constants, so it has to compile and run against
// the pre-fix source to show what it reds on. A test that only fails to compile
// against the old code is not a reproduction of the old behaviour — it is a
// missing helper wearing a test's name.
//
// Duplicating the literal is also the check: if the variable the deployment
// renders ever moves, the ceiling these cases measure stops following the node
// and the expectations below fail rather than silently passing on a bound that
// no longer arrives.
const (
	nodeBytesVar = "ERUN_BUILD_CACHE_NODE_BYTES"
	coTenantsVar = "ERUN_BUILD_CACHE_CO_TENANTS"
)

// nodeBoundCase drives one pass of the retention decision for an environment
// that has told the build how large its node is and how many caches share it.
type nodeBoundCase struct {
	name string
	// nodeGi is the whole node's disk, as the deployment declared it.
	nodeGi uint64
	// coTenants is how many environments' build caches that node holds.
	coTenants uint64
	// volumeGi is the docker volume the chart declares for this environment —
	// the only input the bound used before the node's was wired, and the one
	// that is still the ceiling wherever it is the tighter of the two.
	volumeGi uint64
	// cacheGi is what this environment's BuildKit cache holds.
	cacheGi uint64

	// wantPrunedTo is the ceiling the reclaim must be bounded to. Zero means no
	// reclaim is expected at all.
	wantPrunedTo uint64
}

func nodeBoundCases() []nodeBoundCase {
	return []nodeBoundCase{
		{
			// The reproduction. Four environments on a 120 GiB node, each
			// holding the chart's default 50 GiB docker volume, each therefore
			// allowed 80% of it — 40 GiB apiece, 160 GiB between them, before
			// anything on the node has been asked how much room there is. The
			// node's own reserve is 20 GiB, so the four ceilings together sit
			// 60 GiB past what the node can hold, and every one of the four
			// environments is inside its own bound the whole way.
			//
			// The cache is past both the node-derived mark and the volume one,
			// so a reclaim happens either way: the defect this measures is the
			// bound the reclaim is given, not whether one ran.
			name:         "four environments each inside their own volume ceiling still exceed the node",
			nodeGi:       120,
			coTenants:    4,
			volumeGi:     50,
			cacheGi:      45,
			wantPrunedTo: 20 << 30,
		},
		{
			// A node's bound only ever tightens. Four environments on a node
			// large enough that each one's share is still bigger than the
			// volume it was given keep exactly the ceiling they had: the volume
			// share is the binding one here, and the node's must not replace
			// it.
			name:         "a node share larger than the volume keeps the volume ceiling",
			nodeGi:       400,
			coTenants:    4,
			volumeGi:     50,
			cacheGi:      45,
			wantPrunedTo: 40 << 30,
		},
		{
			// The count is what makes the division a bound on the sum. A larger
			// node shared by proportionally more caches gives each of them the
			// same allowance, so the same ceiling — a mechanic that ignored the
			// count would read this node's whole budget as one environment's
			// and bound nothing.
			name:         "the ceiling follows the node and the cache count together",
			nodeGi:       170,
			coTenants:    6,
			volumeGi:     50,
			cacheGi:      45,
			wantPrunedTo: 20 << 30,
		},
		{
			// A node size with no cache count is an incomplete declaration, not
			// a count of one. Reading it as one would hand a single environment
			// the node's whole budget, which is the unbounded shape the node
			// bound exists to remove — so it stays unapplied and the volume
			// share holds alone.
			name:         "a node size with no cache count does not bound anything",
			nodeGi:       100,
			coTenants:    0,
			volumeGi:     50,
			cacheGi:      45,
			wantPrunedTo: 40 << 30,
		},
		{
			// No node declared at all is how every environment ran before this
			// bound existed, and how one that has said nothing about its node
			// still has to run: the volume share is unchanged.
			name:         "an environment that declares no node keeps its volume ceiling",
			nodeGi:       0,
			coTenants:    0,
			volumeGi:     50,
			cacheGi:      45,
			wantPrunedTo: 40 << 30,
		},
		{
			// A cache inside every mark is left alone, node declared or not:
			// the node bound changes where the ceiling sits, never whether
			// growth below it is reclaimed.
			name:         "a cache under the node-derived mark is left alone",
			nodeGi:       120,
			coTenants:    4,
			volumeGi:     50,
			cacheGi:      10,
			wantPrunedTo: 0,
		},
	}
}

// TestBuildCacheCeilingsStayBoundedAcrossTheNodesEnvironments is the
// reproduction of the aggregate defect: every environment bounds itself and the
// node still fills, because the sum of the per-environment ceilings was never
// anything the code looked at.
//
// The environment bound itself correctly before this change — `resolveBuildCacheBounds`
// caps it at a share of its own docker volume and reclaims when it crosses —
// and that is exactly why the defect was invisible from inside any one build:
// each of them obeys, and 4 x 40 GiB of obedient ceilings is 160 GiB of a node
// that has 80 GiB to give.
//
// The cases below run the production decision path and assert on the bound the
// reclaim is actually given, so what they red on before the fix is the ceiling
// this environment hands `docker buildx prune` — not a symbol that had not been
// written yet. The last four cases are controls: they pin the states that must
// not move when the node bound does (an undeclared node, an under-declared one,
// a node share the volume already tightens, and a cache under every mark).
func TestBuildCacheCeilingsStayBoundedAcrossTheNodesEnvironments(t *testing.T) {
	for _, tc := range nodeBoundCases() {
		t.Run(tc.name, func(t *testing.T) {
			runNodeBoundCase(t, tc)
		})
	}
}

// nodeReserveGi is what the node holds back before any cache may claim room:
// the same floor the disk-headroom preflight refuses and prunes against,
// resolved by the same function, so a ceiling derived from it cannot walk the
// node onto the eviction threshold the floor exists to keep it off.
func nodeReserveGi(nodeGi uint64) uint64 {
	return resolveMinDiskHeadroomBytes(nodeGi<<30) >> 30
}

func runNodeBoundCase(t *testing.T, tc nodeBoundCase) {
	t.Helper()

	// The floor override is cleared so the reserve below is the derived one
	// rather than whatever the machine running the test happens to export.
	t.Setenv(releaseMinDiskHeadroomEnv, "")
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
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, logs, logs)}
	ensureBuildCacheRetentionWith(ctx, buildDiskHeadroomPolicy,
		func() (uint64, error) { return tc.volumeGi << 30, nil },
		func(time.Duration) (uint64, error) { return tc.cacheGi << 30, nil },
		prune)

	if prunedTo != tc.wantPrunedTo {
		t.Fatalf("the cache was reclaimed to %s, want the ceiling %s; output was %q",
			formatGiB(prunedTo), formatGiB(tc.wantPrunedTo), logs.String())
	}
	if tc.wantPrunedTo == 0 {
		return
	}

	// The defect stated as the invariant it broke. Each environment staying
	// inside its own ceiling is not the property that matters — the sum of
	// every co-tenant's ceiling against what the node has left after its own
	// reserve is.
	if tc.coTenants == 0 {
		return
	}
	reserve := nodeReserveGi(tc.nodeGi)
	if tc.nodeGi <= reserve {
		t.Fatalf("a node of %d GiB has no room above its %d GiB reserve to divide", tc.nodeGi, reserve)
	}
	budget := (tc.nodeGi - reserve) << 30
	if spent := prunedTo * tc.coTenants; spent > budget {
		t.Fatalf("the %d environments' ceilings sum to %s, past the %s a %d GiB node has above its %d GiB reserve: each environment is inside its own bound and the node fills anyway",
			tc.coTenants, formatGiB(spent), formatGiB(budget), tc.nodeGi, reserve)
	}
}
