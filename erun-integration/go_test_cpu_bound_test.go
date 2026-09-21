package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

// goTestTargetCountPattern reads the divisor that gives each Go test runner in
// check-gate's -j fan-out its share of the environment's CPU quota.
var goTestTargetCountPattern = regexp.MustCompile(`(?m)^GO_TEST_TARGET_COUNT\s*:?=\s*(\d+)\s*$`)

// Some Go test runners in check-gate's -j fan-out are counted by
// GO_TEST_TARGET_COUNT -- the divisor that hands each of them a share of the
// environment's CPU quota -- and some are not. The integration suite used to
// be the second kind: it is a sibling of the module targets in the same -j
// fan-out, but sized its `-parallel` from `parallel-gate.sh width 32 ""`,
// which resolves the whole quota. On a 6-CPU pod that is 6 for a runner that
// was already sharing the fan-out with five counted targets of 1, so the Go
// test family alone demanded 13 against a cap of 6; once lint, frontend, helm
// and playwright are counted the worst-case batch reached 34. The observable
// consequence is an integration scenario that fails in the gate on a supervisor
// heartbeat deadline while both trees pass in isolation: the suite is being
// throttled by siblings it is not accounted with.
//
// This gate asserts the real wiring rather than a computed width, the same
// way TestGoTestTargetsBoundTheirCPUFanOut does: the divisor must equal the
// number of Go test runners the gate actually starts, the recipe must hand
// its share down to the script, and the script must consume it. It does not
// assert the numeric share -- that is derived from the run's own quota at run
// time, and pinning it would assert the environment instead of the contract.
func TestIntegrationSuiteSharesTheGoTestCPUBudget(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	makefileText := readMakefile(t, root)

	// The gate starts len(goTestTargets) counted module targets plus the
	// integration suite. A divisor below that hands the uncounted runner no
	// share at all; above it, every runner is undersold the share it could
	// have taken.
	wantTargets := len(goTestTargets) + 1
	m := goTestTargetCountPattern.FindStringSubmatch(makefileText)
	if m == nil {
		t.Fatalf("the Makefile no longer defines GO_TEST_TARGET_COUNT: it is the divisor that gives each Go " +
			"test runner in check-gate's -j fan-out its share of the CPU quota, so without it each one claims " +
			"the whole quota again")
	}
	count, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("GO_TEST_TARGET_COUNT is not an integer: %q", m[1])
	}
	if count != wantTargets {
		t.Errorf("GO_TEST_TARGET_COUNT is %d, but check-gate runs %d targets that fan out into concurrent "+
			"`go test` processes (%s, integration-test-gate): the count is the divisor that gives each of them "+
			"its share of the CPU quota, so a fan-out outside it takes a whole-quota share on top of the shares "+
			"the counted ones already demand",
			count, wantTargets, strings.Join(goTestTargets, ", "))
	}

	// The suite's width arrives through a script, not a recipe line, so the
	// share has to cross that boundary explicitly.
	recipe := makeTargetRecipe(t, makefileText, "integration-test-gate")
	if !strings.Contains(recipe, "GO_TEST_GOMAXPROCS=$(GO_TEST_GOMAXPROCS)") {
		t.Errorf("integration-test-gate no longer passes GO_TEST_GOMAXPROCS=$(GO_TEST_GOMAXPROCS) to " +
			"erun-integration/scripts/integration-test.sh: without it the suite sizes its -parallel against " +
			"the whole resolved quota, which is concurrent with the shares the counted module targets take")
	}

	const scriptPath = "erun-integration/scripts/integration-test.sh"
	raw, err := os.ReadFile(filepath.Join(root, scriptPath))
	if err != nil {
		t.Fatal(err)
	}
	assignment := testParallelismAssignment(t, string(raw), scriptPath)
	// Read the precedence out of the assignment rather than matching one
	// spelling of the line: a reflowed expression that still resolves the
	// whole quota first would otherwise slip past a containment check that
	// had silently stopped meaning anything.
	overrideAt := strings.Index(assignment, "INTEGRATION_TEST_PARALLELISM")
	shareAt := strings.Index(assignment, "GO_TEST_GOMAXPROCS")
	if overrideAt < 0 || shareAt < 0 || overrideAt > shareAt {
		t.Errorf("%s decides its width as %q, but the gate's per-target share has to be the default that "+
			"applies when nothing overrides it: the module targets beside this suite in check-gate's -j "+
			"fan-out take GO_TEST_TARGET_COUNT-sized shares of the same quota, so a suite that falls through "+
			"to the whole quota claims it a second time on top of theirs", scriptPath, strings.TrimSpace(assignment))
	}
	if shareAt >= 0 {
		fallback := assignment[shareAt:]
		if !strings.Contains(fallback, "parallel-gate.sh") || !strings.Contains(fallback, "width") {
			t.Errorf("%s decides its width as %q, but the quota-derived width has to remain the fallback: "+
				"only a standalone run has no sibling competing for that quota, and it still needs a real width",
				scriptPath, strings.TrimSpace(assignment))
		}
	}
}

// testParallelismAssignment returns the line in integration-test.sh that sets
// test_parallelism -- the shell expression the suite's -parallel is resolved
// from -- so a caller can assert the precedence inside it. It fails rather
// than returning an empty string: a silent zero value here would turn every
// assertion about that expression into a vacuous pass, which is the failure
// mode the assertions exist to avoid.
func testParallelismAssignment(t testing.TB, scriptText, scriptPath string) string {
	t.Helper()
	for _, line := range strings.Split(scriptText, "\n") {
		if strings.HasPrefix(line, "test_parallelism=") {
			return line
		}
	}
	t.Fatalf("%s no longer assigns test_parallelism, so nothing decides the suite's -parallel", scriptPath)
	return ""
}
