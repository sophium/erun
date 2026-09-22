package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-integration/internal/harnessexec"
)

// The integration suite is the gate's longest Go test run and its only one
// that takes a wall-clock budget from outside the test binary. Left alone it
// inherits Go's ten-minute default, which is fixed while the suite's duration
// is not: the same suite measured 4m7s on a quiet pod and crossed ten minutes
// on a contended one, where the goroutine dump at the alarm showed dozens of
// t.Parallel() scenarios still queued and the package still progressing. The
// run was failed by its own clock rather than by its tree, which is the one
// thing a gate must not do.
//
// This gate keeps that budget from being dropped by a later edit, the same way
// TestIntegrationSuiteSharesTheGoTestCPUBudget keeps the suite's -parallel
// share from being dropped: it reads the Makefile's and the script's real text
// rather than executing them. It deliberately does not assert the numeric
// budget -- that is derived from the run's own CPU quota, and pinning it would
// assert the environment instead of the contract. What it asserts is that the
// budget is derived rather than constant, that it actually reaches `go test`,
// and that it stays below the backstop that is sized against it.
func TestIntegrationSuiteTimeoutBudgetIsDerivedAndCappedUnderTheHangNet(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	makefileText := readMakefile(t, root)

	// An overridable ("?=") definition, so an operator can hand a single run a
	// different budget -- the same escape hatch LINT_TIMEOUT keeps.
	if !regexp.MustCompile(`(?m)^INTEGRATION_TEST_TIMEOUT\s*\?=`).MatchString(makefileText) {
		t.Fatalf("the Makefile no longer defines INTEGRATION_TEST_TIMEOUT as an overridable (\"?=\") " +
			"variable: without it the suite falls back to Go's fixed ten-minute default, which fails a " +
			"correct tree whenever the node is slow enough")
	}
	// Derived from the environment's real quota, not a constant: the quota is
	// what GO_TEST_GOMAXPROCS already divides into this suite's -parallel
	// share, so a constant here would under-serve a small environment and
	// over-serve a large one. Read the definition itself rather than the file
	// around it -- LINT_TIMEOUT above reads the same cpu-quota helper, so a
	// whole-file search would still pass on a definition that had been
	// replaced outright with a literal.
	definition := makeVariableDefinition(t, makefileText, "INTEGRATION_TEST_TIMEOUT")
	if !strings.Contains(definition, "parallel-gate.sh cpu-quota") {
		t.Errorf("INTEGRATION_TEST_TIMEOUT is defined as:\n  %s\nwhich no longer derives its budget from the "+
			"environment's real CPU quota (parallel-gate.sh cpu-quota): the suite's wall clock is a function "+
			"of the CPU it actually gets, so a budget that does not read that quota is a guess",
			strings.TrimSpace(definition))
	}
	if !strings.Contains(definition, "INTEGRATION_TEST_TIMEOUT_CAP_MINUTES") {
		t.Errorf("INTEGRATION_TEST_TIMEOUT is defined as:\n  %s\nwhich no longer clamps to "+
			"INTEGRATION_TEST_TIMEOUT_CAP_MINUTES: the cap is what keeps the deadline below the backstop "+
			"harnessexec.HangNet is sized against, so dropping it reopens that bound", strings.TrimSpace(definition))
	}
	base := makeIntVariable(t, makefileText, "INTEGRATION_TEST_TIMEOUT_BASE_MINUTES")
	capMinutes := makeIntVariable(t, makefileText, "INTEGRATION_TEST_TIMEOUT_CAP_MINUTES")
	if base > capMinutes {
		t.Errorf("INTEGRATION_TEST_TIMEOUT_BASE_MINUTES is %d but INTEGRATION_TEST_TIMEOUT_CAP_MINUTES is %d: "+
			"the base is the floor of the derivation and the cap is its ceiling, so a base above the cap "+
			"makes the stated cap a value the expression can never honour", base, capMinutes)
	}

	// The budget has to cross the recipe/script boundary explicitly, or the
	// script resolves its own fallback and the derivation above is dead text.
	recipe := makeTargetRecipe(t, makefileText, "integration-test-gate")
	if !strings.Contains(recipe, "INTEGRATION_TEST_TIMEOUT=$(INTEGRATION_TEST_TIMEOUT)") {
		t.Errorf("integration-test-gate no longer passes INTEGRATION_TEST_TIMEOUT=$(INTEGRATION_TEST_TIMEOUT) to " +
			"erun-integration/scripts/integration-test.sh: without it the suite runs under Go's fixed " +
			"ten-minute default instead of the budget derived above")
	}

	const scriptPath = "erun-integration/scripts/integration-test.sh"
	raw, err := os.ReadFile(filepath.Join(root, scriptPath))
	if err != nil {
		t.Fatal(err)
	}
	scriptText := string(raw)

	// Read the precedence out of the assignment rather than matching one
	// spelling of the line, for the same reason the -parallel assertion does:
	// a reflowed expression that stopped honouring the gate's value would
	// otherwise slip past a containment check that had silently stopped
	// meaning anything.
	assignment := testTimeoutAssignment(t, scriptText, scriptPath)
	if !strings.Contains(assignment, "INTEGRATION_TEST_TIMEOUT:-") {
		t.Errorf("%s decides its timeout as %q, but the gate's derived budget has to be what applies when "+
			"nothing overrides it", scriptPath, strings.TrimSpace(assignment))
	}

	// Every `go test` this script runs must carry the budget, including the
	// --update-golden path: a reseed is the same suite under the same clock.
	for _, line := range strings.Split(scriptText, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || !strings.Contains(line, "go test ") {
			continue
		}
		if !strings.Contains(line, `-timeout="$test_timeout"`) {
			t.Errorf("%s runs `go test` without the resolved -timeout budget, on the line:\n  %s\n"+
				"Go's ten-minute default is what failed a correct tree, so every invocation that can be "+
				"slow needs the budget, not just the gated one", scriptPath, trimmed)
		}
	}

	// The resolved budget is printed with the run, so a gate log says how much
	// clock the suite had rather than leaving a reader to infer it from the
	// goroutine dump of a run that ran out.
	if !strings.Contains(scriptText, "timeout: $test_timeout") {
		t.Errorf("%s no longer prints the resolved timeout in its banner: a run that is slow and a run "+
			"that is stuck are indistinguishable in the gate log without it", scriptPath)
	}

	// The invariant the cap exists for. harnessexec.HangNet is deliberately
	// longer than the package deadline so it can never fail a healthy child;
	// an uncapped inverse scale would climb past it on a small environment and
	// reopen that failure inside the harness instead of in the clock. Read the
	// real constant rather than a copy of it, so moving HangNet alone fails
	// here rather than silently reordering the two bounds.
	capDeadline := time.Duration(capMinutes) * time.Minute
	if capDeadline >= harnessexec.HangNet {
		t.Errorf("INTEGRATION_TEST_TIMEOUT caps the suite's package deadline at %s, but "+
			"harnessexec.HangNet is %s: the backstop has to outlast every deadline the gate can "+
			"resolve, or it starts cancelling children in a run that still had clock left",
			capDeadline, harnessexec.HangNet)
	}

	// The script's own fallback is the one path that does not come through the
	// Makefile, so it is the one that can drift past the cap unnoticed.
	fallback := testTimeoutFallback(t, assignment)
	if fallback > capDeadline {
		t.Errorf("%s falls back to a %s timeout when INTEGRATION_TEST_TIMEOUT is unset, above the %s cap "+
			"the Makefile clamps its derived budget to: a direct run would then exceed the deadline "+
			"harnessexec.HangNet is sized against, which the cap exists to prevent",
			scriptPath, fallback, capDeadline)
	}
}

// testTimeoutAssignment returns the line in integration-test.sh that sets
// test_timeout -- the shell expression the suite's -timeout is resolved from --
// so a caller can assert the precedence inside it. It fails rather than
// returning an empty string: a silent zero value here would turn every
// assertion about that expression into a vacuous pass, which is the failure
// mode the assertions exist to avoid.
func testTimeoutAssignment(t testing.TB, scriptText, scriptPath string) string {
	t.Helper()
	for _, line := range strings.Split(scriptText, "\n") {
		if strings.HasPrefix(line, "test_timeout=") {
			return line
		}
	}
	t.Fatalf("%s no longer assigns test_timeout, so nothing decides the suite's -timeout budget", scriptPath)
	return ""
}

// testTimeoutFallback reads the value the assignment falls back to when
// INTEGRATION_TEST_TIMEOUT is unset, and fails if it is not a duration this
// test can compare -- an unparseable fallback would make the comparison above
// a vacuous pass rather than a finding.
func testTimeoutFallback(t testing.TB, assignment string) time.Duration {
	t.Helper()
	idx := strings.Index(assignment, "INTEGRATION_TEST_TIMEOUT:-")
	if idx < 0 {
		t.Fatalf("the test_timeout assignment %q no longer names INTEGRATION_TEST_TIMEOUT as what it "+
			"falls back from, so there is no fallback to read", strings.TrimSpace(assignment))
	}
	rest := assignment[idx+len("INTEGRATION_TEST_TIMEOUT:-"):]
	end := strings.Index(rest, "}")
	if end < 0 {
		t.Fatalf("the test_timeout assignment %q has no closing brace after its default, so the "+
			"fallback cannot be read", strings.TrimSpace(assignment))
	}
	d, err := time.ParseDuration(rest[:end])
	if err != nil {
		t.Fatalf("the test_timeout fallback in %q is not a parsable duration: %v", strings.TrimSpace(assignment), err)
	}
	return d
}

// makeVariableDefinition returns a Makefile variable's full definition,
// including any backslash-continued lines, so a caller can assert what that
// one definition does rather than what the file mentions somewhere. It fails
// rather than returning an empty string: an empty definition would turn every
// containment assertion about it into a vacuous pass, and would also make the
// negated ones pass for the wrong reason.
func makeVariableDefinition(t testing.TB, makefileText, name string) string {
	t.Helper()
	lines := strings.Split(makefileText, "\n")
	pattern := regexp.MustCompile(`^` + regexp.QuoteMeta(name) + `\s*[:?]?=`)
	for i, line := range lines {
		if !pattern.MatchString(line) {
			continue
		}
		definition := line
		for strings.HasSuffix(strings.TrimRight(definition, " \t"), "\\") && i+1 < len(lines) {
			i++
			definition += "\n" + lines[i]
		}
		return definition
	}
	t.Fatalf("the Makefile no longer defines %s, so what its definition does cannot be asserted", name)
	return ""
}

// makeIntVariable reads a single-line, literal integer Makefile variable so a
// caller can compare it. It fails rather than returning a zero, for the same
// reason the readers above do: a zero would satisfy a "< cap" comparison that
// the real value does not.
func makeIntVariable(t testing.TB, makefileText, name string) int {
	t.Helper()
	pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `\s*:?=\s*(\S+)\s*$`)
	m := pattern.FindStringSubmatch(makefileText)
	if m == nil {
		t.Fatalf("the Makefile no longer defines %s as a single-line value, so it cannot be compared", name)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("the Makefile defines %s as %q, which is not an integer", name, m[1])
	}
	return n
}
