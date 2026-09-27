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
			// The reproduction of the zero. erun's disk-headroom floor is a flat
			// 20 GiB for every node below ten times that, so a node of 16 GiB
			// reserves more than it has and leaves nothing above the floor to
			// divide. The arithmetic that reaches that conclusion must not come
			// back as an allowance of zero: zero is not a small bound, it is a
			// ceiling of no bytes at all, and the reclaim bounded by it is
			// `--max-used-space 0` — every earned layer dropped on every build,
			// with the ceiling never rising afterwards, because a cache holding
			// nothing is never over a ceiling of nothing.
			name:         "a node at or below its own disk floor does not bound the cache to nothing",
			nodeGi:       16,
			coTenants:    4,
			volumeGi:     50,
			cacheGi:      45,
			wantPrunedTo: 40 << 30,
		},
		{
			// The same state on the boundary itself: the floor is the largest a
			// node may be and still have nothing above it, and a ceiling that
			// flips between the volume's share and nothing at all across that
			// line is the defect, not the line.
			name:         "a node exactly at its disk floor does not bound the cache to nothing",
			nodeGi:       20,
			coTenants:    4,
			volumeGi:     50,
			cacheGi:      45,
			wantPrunedTo: 40 << 30,
		},
		{
			// The control for those two: a node just above its floor still has
			// room to divide, and the bound must still tighten. Without this the
			// fix could satisfy the cases above by disabling the node bound
			// outright, which is the opposite of what it is for.
			// 21 GiB less the flat 20 GiB floor is 1 GiB of room, a quarter of
			// it is 268435456 bytes, and 80% of that is the ceiling below —
			// stated as the number rather than as the formula, so the case pins
			// the rendered ceiling instead of restating the code it measures.
			name:         "a node just above its floor still tightens the ceiling",
			nodeGi:       21,
			coTenants:    4,
			volumeGi:     50,
			cacheGi:      45,
			wantPrunedTo: 214748320,
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

// TestBuildCacheRetentionDoesNotBoundToANodeShareOfNoBytes covers the other way
// a node share resolves to a ceiling of zero bytes, and the one the node's own
// floor does not explain: a node with room above its floor whose share divides
// down to nothing. `resolveBuildCacheBounds` divides before it multiplies, as
// the disk floor does, so a share of anything under a hundred bytes has a
// ceiling of zero however much room the node had.
//
// The numbers are byte-scale rather than gigabyte-scale on purpose — a share
// that lands here is of the same order as the floor and the count, which no
// node anyone declares is — and the floor travels as its override for the same
// reason. What is asserted is the same property as the cases above: a ceiling
// the reclaim would be bounded to is either the volume share or a bound
// something can live under, never zero.
func TestBuildCacheRetentionDoesNotBoundToANodeShareOfNoBytes(t *testing.T) {
	const (
		floorBytes  = 100
		nodeBytes   = 250
		coTenants   = 4
		volumeGi    = 50
		volumeBytes = volumeGi << 30
		cacheBytes  = 45 << 30
	)
	t.Setenv(releaseMinDiskHeadroomEnv, strconv.FormatUint(floorBytes, 10))
	t.Setenv(nodeBytesVar, strconv.FormatUint(nodeBytes, 10))
	t.Setenv(coTenantsVar, strconv.FormatUint(coTenants, 10))

	var prunedTo uint64
	logs := &strings.Builder{}
	ensureBuildCacheRetentionWith(Context{Logger: NewLoggerWithWriters(VerbosityInfo, logs, logs)}, buildDiskHeadroomPolicy,
		func() (uint64, error) { return volumeBytes, nil },
		func(time.Duration) (uint64, error) { return cacheBytes, nil },
		func(ceiling uint64, _ time.Duration) error { prunedTo = ceiling; return nil })

	want := resolveBuildCacheBounds(volumeBytes).ceiling
	if prunedTo != want {
		t.Fatalf("a node share with a ceiling of no bytes was reclaimed to %s, want the volume ceiling %s; output was %q",
			formatGiB(prunedTo), formatGiB(want), logs.String())
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
	// A node at or below its own reserve has nothing to divide, so no share of
	// it is in force and there is no sum of node-derived ceilings to add up.
	// The ceiling assertion above is the whole check for those cases; the
	// invariant below is about the cases where a share did apply.
	if tc.coTenants == 0 || tc.nodeGi <= nodeReserveGi(tc.nodeGi) {
		return
	}
	reserve := nodeReserveGi(tc.nodeGi)
	budget := (tc.nodeGi - reserve) << 30
	if spent := prunedTo * tc.coTenants; spent > budget {
		t.Fatalf("the %d environments' ceilings sum to %s, past the %s a %d GiB node has above its %d GiB reserve: each environment is inside its own bound and the node fills anyway",
			tc.coTenants, formatGiB(spent), formatGiB(budget), tc.nodeGi, reserve)
	}
}
