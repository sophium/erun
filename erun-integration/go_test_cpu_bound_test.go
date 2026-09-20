package integration

import (
	"strings"
	"testing"
)

// goTestTargets are the Makefile targets whose recipe runs `go test ./...`
// over a module from inside `make check`'s own -j fan-out. Each one is a
// separate target, so they are concurrent siblings there rather than a single
// bounded batch -- which is precisely why each has to bound its own fan-out.
var goTestTargets = []string{
	"test-erun-common",
	"test-erun-ui",
	"test-erun-backend-api",
	"test-erun-mcp",
	"test-erun-dns01-webhook",
}

// `go test ./...` builds and runs package test binaries up to GOMAXPROCS at a
// time, and takes that number from the cgroup when nothing sets it. Several of
// these targets running concurrently in the gate therefore each claim the
// environment's whole CPU quota, and together they demand a multiple of it:
// measured on the 6-CPU in-pod gate arrangement (lint plus all four module test
// targets, warm build cache, -j5), the unbounded arrangement throttled 13.4% of
// CPU periods for 127s of throttled CPU-time, against 3.6% and 6.1s once each
// target was held to a shared quota -- the same wall clock either way, so the
// throttling bought nothing and is what turns into the syscall-timeout-shaped
// failures that read as network faults.
//
// This gate keeps the bound from being dropped by a later edit, the same way
// TestBuildCheckGateCoversEveryTestSuite keeps a module from falling out of
// `make check`: it reads the Makefile's real text rather than executing it, and
// fails on the pre-fix recipe. It deliberately does not assert the numeric
// width -- that is derived from the environment's own CPU quota at run time,
// and pinning it here would be asserting the environment instead of the
// contract. What it asserts is that every Go test target the gate runs
// actually names the shared bound, and that the bound is really defined as a
// quota-derived value rather than a constant that would oversubscribe a small
// environment.
func TestGoTestTargetsBoundTheirCPUFanOut(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	makefileText := readMakefile(t, root)

	if !strings.Contains(makefileText, "GO_TEST_GOMAXPROCS ?=") {
		t.Error("the Makefile no longer defines GO_TEST_GOMAXPROCS as an overridable (\"?=\") variable -- " +
			"without it nothing bounds the Go test targets' internal parallelism")
	}
	if !strings.Contains(makefileText, "parallel-gate.sh cpu-quota") ||
		!strings.Contains(makefileText, "GO_TEST_TARGET_COUNT") {
		t.Error("GO_TEST_GOMAXPROCS no longer derives its width from the environment's real CPU quota " +
			"(parallel-gate.sh cpu-quota divided by GO_TEST_TARGET_COUNT) -- a constant here would " +
			"oversubscribe a smaller environment and underuse a larger one")
	}

	for _, target := range goTestTargets {
		recipe := makeTargetRecipe(t, makefileText, target)
		if !strings.Contains(recipe, "go test") {
			t.Errorf("%s's recipe no longer runs `go test` -- the target list in this gate has gone stale", target)
			continue
		}
		if !strings.Contains(recipe, "GOMAXPROCS=$(GO_TEST_GOMAXPROCS)") {
			t.Errorf("%s runs `go test` without GOMAXPROCS=$(GO_TEST_GOMAXPROCS): its unbounded package "+
				"parallelism (up to the whole CPU quota) is concurrent with the other %d Go test targets in "+
				"check-gate's -j fan-out, so together they demand a multiple of the environment's quota and "+
				"spend CPU periods throttled", target, len(goTestTargets)-1)
		}
	}
}
