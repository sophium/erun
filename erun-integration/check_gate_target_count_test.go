package integration

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// checkGateTargetCountPattern reads the Makefile's own statement of how many
// targets check-gate fans out over.
var checkGateTargetCountPattern = regexp.MustCompile(`(?m)^CHECK_GATE_TARGET_COUNT\s*:?=\s*(\d+)\s*$`)

// CHECK_GATE_TARGET_COUNT is one number written twice: as a constant, and as
// check-gate's own prerequisite list. It is the job-count term
// `parallel-gate.sh width` takes, so it exists to stop `-j` opening more slots
// than there are jobs to fill them -- `min()` bounds it against the CPU quota
// too, so a too-low value is the only direction it can be wrong in, and it
// under-counts silently: every target past the stale value still runs, just
// queued behind a free slot rather than in it.
//
// That is exactly what happened. The constant was correct at 10 when it was
// introduced, then test-erun-common and test-atlas-validate joined the list
// without it being bumped, and the gate resolved -j10 for twelve targets
// because the in-pod CPU term happened to be the larger one. Nothing failed;
// the fan-out was simply narrower than it said it was.
//
// This gate reads the Makefile's real text rather than executing it (the same
// approach as TestBuildCheckGateCoversEveryTestSuite and
// TestIntegrationSuiteSharesTheGoTestCPUBudget beside it), and fails on the
// pre-fix Makefile. It is not a substitute for the coverage gates that check
// *which* targets are listed -- it only keeps the count of them and the
// constant that claims to be that count from drifting apart again.
func TestCheckGateTargetCountMatchesItsPrerequisites(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	makefileText := readMakefile(t, root)

	prereqs := makeTargetPrerequisites(t, makefileText, "check-gate")
	// A parse that silently returned nothing would make the comparison below
	// vacuous, so refuse to compare against an empty list at all.
	if len(prereqs) == 0 {
		t.Fatal("check-gate has no prerequisites, so this test would compare the constant against nothing: " +
			"either the target was emptied or the prerequisite parse above stopped matching, and both need " +
			"fixing here rather than weakening")
	}

	m := checkGateTargetCountPattern.FindStringSubmatch(makefileText)
	if m == nil {
		t.Fatalf("the Makefile no longer defines CHECK_GATE_TARGET_COUNT: it is the job-count term " +
			"CHECK_GATE_PARALLELISM resolves through `parallel-gate.sh width`, so without it the " +
			"fan-out is sized by the CPU quota alone")
	}
	count, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("CHECK_GATE_TARGET_COUNT is not an integer: %q", m[1])
	}
	if count != len(prereqs) {
		t.Errorf("CHECK_GATE_TARGET_COUNT is %d, but check-gate declares %d prerequisites (%s): the constant "+
			"is the job-count term `-j` is resolved from, so a target past it takes a free slot only once an "+
			"earlier one finishes instead of starting in the first dispatch batch",
			count, len(prereqs), strings.Join(prereqs, " "))
	}

	// The comparison above is only about the number if the number is what the
	// fan-out is actually resolved from: a constant left behind while
	// CHECK_GATE_PARALLELISM moved to some other term would pass it vacuously.
	if !strings.Contains(makefileText, "parallel-gate.sh width $(CHECK_GATE_TARGET_COUNT)") {
		t.Error("CHECK_GATE_PARALLELISM no longer resolves its width from $(CHECK_GATE_TARGET_COUNT), so the " +
			"count above is no longer the term `-j` uses")
	}
}
