package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// minimumCoverageMargin is the floor this gate must keep between the total the
// suite measures on main and the threshold it enforces. The comparison runs on
// the one-decimal total `go tool cover -func` prints, and 0.1 points is 37 of
// the ~37,000 statements the profile covers, so one tenth is the smallest
// margin that exists at all in that comparison -- and this suite already held a
// 0.1-point margin once (75.8 pinned against a measured 75.9) before being
// re-based to 0.3 as too thin for host variance. A re-base that genuinely needs
// less than this is a change to this bound, and belongs in the PR discussion
// that argues for it rather than in a quiet edit to the script.
const minimumCoverageMargin = 0.2

// maximumCoverageMargin bounds the other direction. The margin exists to absorb
// variance between hosts, not to buy the suite room to regress: a margin of a
// point or more would swallow the coverage drops this gate exists to catch
// (the largest one actually measured here was 0.4 points, on a branch adding
// uncovered production code).
const maximumCoverageMargin = 1.0

var (
	coverageMeasuredLinePattern = regexp.MustCompile(`(?m)^coverage_measured=([0-9]+(?:\.[0-9]+)?)$`)
	coverageMarginLinePattern   = regexp.MustCompile(`(?m)^coverage_margin=([0-9]+(?:\.[0-9]+)?)$`)
)

// TestIntegrationCoverageGateKeepsItsMargin keeps the coverage gate's margin
// from being consumed a third time.
//
// The threshold used to be a bare literal whose stated intent lived only in the
// script's header ("what the suite actually reaches, minus a small margin for
// cross-host variance"), so the suite's own growth walked it down one printed
// tenth at a time with nothing to notice the drift. It ended up sitting on the
// measured value exactly: main reported 75.107933% over 37,060 statements
// against a threshold of 75.1%, i.e. about three statements of headroom, and a
// branch with real uncovered code arrived at a gate with none. The script now
// names the pair (`coverage_measured` and `coverage_margin`) and derives the
// default from it.
//
// This reads that real text rather than executing the script, the same way
// TestGoTestTargetsBoundTheirCPUFanOut reads the Makefile rather than running
// it: it cannot know what the suite measures today, only that whatever the
// script claims it measures keeps a margin below the bar it enforces. What pins
// the rendered comparison and its failure message is
// scripts/integration-test_test.sh, which runs against a stubbed `go`.
func TestIntegrationCoverageGateKeepsItsMargin(t *testing.T) {
	t.Parallel()
	script := readIntegrationGateScript(t)

	measured := shellNumberAssignment(t, script, coverageMeasuredLinePattern, "coverage_measured")
	margin := shellNumberAssignment(t, script, coverageMarginLinePattern, "coverage_margin")

	if margin < minimumCoverageMargin {
		t.Errorf("the coverage gate keeps a margin of only %g points below the measured total: "+
			"a margin under %g does not survive the one-decimal comparison this gate makes (0.1 points "+
			"is 37 statements of the ~37,000 the profile covers), and this suite measured 0.1 to be too "+
			"thin for host variance before. Raise coverage_margin, or argue for a smaller bound here.",
			margin, minimumCoverageMargin)
	}
	if margin > maximumCoverageMargin {
		t.Errorf("the coverage gate keeps a margin of %g points below the measured total: "+
			"a margin that wide absorbs the coverage drops the gate exists to catch (the largest actually "+
			"measured here was 0.4 points). Keep the margin tight and let coverage_measured carry the "+
			"increase instead.", margin)
	}

	// coverage_measured is the percentage the gate prints, not a fraction of
	// it: the default is derived from this value, so a 0.751 here would pin a
	// threshold of 0.5 and pass every run that ever executes.
	if measured <= 0 || measured > 100 {
		t.Errorf("coverage_measured is %g, which is not a coverage percentage between 0 and 100: "+
			"the default threshold is derived from it, so a fraction here pins a bar nothing can miss", measured)
	}

	// The default has to be derived from the pair rather than written out
	// again: a second literal is free to disagree with the stated margin,
	// which is how the pin came to sit on the measured value unnoticed.
	assignment := strings.TrimSpace(thresholdAssignment(script))
	for _, want := range []string{"COVERAGE_THRESHOLD", "coverage_measured", "coverage_margin"} {
		if !strings.Contains(assignment, want) {
			t.Errorf("the default threshold is no longer derived from %s: the assignment is %q. "+
				"A bare literal here can drift away from the margin the script states, which is the "+
				"drift that left main one printed tenth from failing every gate on every branch.",
				want, assignment)
		}
	}

	// The failure has to name the shortfall, or the next reader re-derives by
	// hand whether a failing branch was close. integration-test_test.sh pins the
	// rendered text; this keeps the reporting from leaving the gate's script.
	if !strings.Contains(script, "is below threshold") || !strings.Contains(script, "short by") {
		t.Error("the coverage gate no longer reports a below-threshold total with its shortfall -- " +
			"the failure must name the measured value, the threshold, and the difference between them")
	}
}

// thresholdAssignment returns the line that sets the gate's `threshold`, which
// is the one assignment (the `-threshold=` and `--threshold=` argument forms
// that also mention the name are matched and skipped).
func thresholdAssignment(script string) string {
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(line, "threshold=") {
			return line
		}
	}
	return ""
}

// readIntegrationGateScript reads the coverage gate's own script, which lives
// beside this module rather than in the compiled inputs of any test.
func readIntegrationGateScript(t testing.TB) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "erun-integration", "scripts", "integration-test.sh")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the coverage gate script at %s: %v", path, err)
	}
	return string(content)
}

// shellNumberAssignment reads a `name=1.23` assignment out of a shell script
// and fails the test if the name is gone or no longer holds a number.
func shellNumberAssignment(t testing.TB, script string, pattern *regexp.Regexp, name string) float64 {
	t.Helper()
	match := pattern.FindStringSubmatch(script)
	if match == nil {
		t.Fatalf("the coverage gate script no longer assigns a numeric %s", name)
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		t.Fatalf("parse %s=%q: %v", name, match[1], err)
	}
	return value
}
