package integration

// The lint target's deadline used to be scaled by the environment's own CPU
// quota, so the same commit's lint was handed a different budget by a CPU-22
// pod (900s, the floor) than by a CPU-8 one (2460s). Both verdicts were real,
// but only one of them was about the code: a lint killed by the shorter
// deadline and a lint that found something produced the same red, and nothing
// in the log said which budget the run had. A reader of that log could not
// tell whether the tree regressed or the pod changed.
//
// The budget is now one number on every environment
// (TestLintBudgetDoesNotVaryWithTheEnvironmentsCPULimit drives that), and the
// quota still decides what does have to vary with the machine -- the fan-out
// width and the per-lint CPU share. So the recipe prints the budget it ran
// under -- the timeout, the fan-out width, the per-lint GOMAXPROCS, and the
// CPU quota the widths were derived from -- and repeats it beside the
// aggregated failure line.
//
// What this pins is not that a line was printed but that the numbers on it are
// the numbers the lint process actually got: the stub golangci-lint below
// reports the --timeout it was handed and the GOMAXPROCS it ran under, and
// every case below compares those against the printed line. A budget printed
// from a second resolution of the formula could disagree with the one passed
// to the modules, which is exactly the failure this line exists to rule out.
//
// It drives the real `lint` recipe over the same helper the verdict test uses,
// so the wiring that prints the line is what is under test, not a copy of it.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestLintTargetPrintsTheResolvedBudgetItRunsUnder(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	pin, err := os.ReadFile(filepath.Join(root, "GOLANGCI_LINT_VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	version := strings.TrimPrefix(strings.TrimSpace(string(pin)), "v")

	// The module directory only has to exist: the stub answers for it.
	moduleDir := t.TempDir()

	// The two pods from the report, driven through the same override the
	// erun-devops Dockerfile uses to thread the sidecar's own CPU limit in.
	deadlines := map[int]string{}
	for _, cores := range []int{22, 8} {
		t.Run(fmt.Sprintf("a %d-core pod's budget is named and is the one the lint got", cores), func(t *testing.T) {
			stubDir := t.TempDir()
			writeReportingLintStub(t, stubDir, version, 0)

			out, err := runLintTarget(t, root, stubDir, moduleDir, "PARALLEL_GATE_CPU_LIMIT="+strconv.Itoa(cores))
			if err != nil {
				t.Fatalf("the lint target failed a stub that exited 0: %v\n%s", err, out)
			}

			budget := parseLintBudget(t, out)

			// The quota the line names has to be the one this run resolved,
			// or the reader is being told about a different environment.
			if want := strconv.Itoa(cores); budget["cpu"] != want {
				t.Errorf("the lint target printed %q as the cpu quota it resolved from, but this run "+
					"carried PARALLEL_GATE_CPU_LIMIT=%s: a budget attributed to the wrong quota is worse "+
					"than an unattributed one, because it looks like an answer\n%s",
					budget["cpu"], want, out)
			}

			// The point of the line: the budget it names is the budget the
			// lint process was actually handed.
			got := stubReport(t, out)
			if budget["LINT_TIMEOUT"] != got["timeout"] {
				t.Errorf("the lint target printed LINT_TIMEOUT=%s but handed the linter --timeout %s: the "+
					"line has to report the deadline the run happened under, not a second resolution of it",
					budget["LINT_TIMEOUT"], got["timeout"])
			}
			if budget["LINT_GOMAXPROCS"] != got["gomaxprocs"] {
				t.Errorf("the lint target printed LINT_GOMAXPROCS=%s but ran the linter under GOMAXPROCS=%s: "+
					"the printed CPU share has to be the one the lint process got",
					budget["LINT_GOMAXPROCS"], got["gomaxprocs"])
			}
			if budget["LINT_PARALLELISM"] == "" {
				t.Errorf("the lint target's budget line does not name LINT_PARALLELISM, so a reader cannot "+
					"tell how many lints shared that quota\n%s", out)
			}

			deadlines[cores] = budget["LINT_TIMEOUT"]
		})
	}

	// The asymmetry this file was written around is gone: two pods, one
	// budget. This guard asserted the opposite until the budget was made
	// uniform -- it existed to keep the cases above from becoming vacuous on a
	// machine whose quota made the scaling term inert, by failing when the two
	// pods agreed. The difference between them was the defect, so the
	// assertion is inverted rather than dropped: a 22-core pod and an 8-core
	// pod running one commit's lint now have to resolve one deadline, or a red
	// stops meaning the same thing depending on which of them produced it.
	if slow, ok := deadlines[22]; ok {
		if fast, ok := deadlines[8]; ok && slow != fast {
			t.Errorf("a 22-core pod resolved LINT_TIMEOUT=%s and an 8-core pod %s: the budget is decided "+
				"by the environment again, so the same commit's lint can pass on one of them and fail on "+
				"the other, and neither red says which happened", slow, fast)
		}
	}
}

// TestLintBudgetDoesNotVaryWithTheEnvironmentsCPULimit is the invariant the
// budget line exists for: the deadline a module's lint is given is a property
// of the gate, not of the machine that happens to be running it. It drives
// the real target at every CPU quota a gate environment resolves -- from 4,
// the smallest erun sizes a build environment at, to 22, the reference the
// budget is calibrated from -- and requires one resolved LINT_TIMEOUT across
// all of them, read back from the stub linter so the number compared is the
// one the module was actually handed rather than a second resolution of the
// formula that produced it.
//
// Quotas the fleet does not currently run at are included on purpose: what is
// asserted is that the value does not move with the quota at all, not that the
// quotas in use today happen to agree.
func TestLintBudgetDoesNotVaryWithTheEnvironmentsCPULimit(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	pin, err := os.ReadFile(filepath.Join(root, "GOLANGCI_LINT_VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	version := strings.TrimPrefix(strings.TrimSpace(string(pin)), "v")

	moduleDir := t.TempDir()
	quotas := []int{4, 8, 12, 16, 22}

	budgets := map[int]string{}
	for _, cores := range quotas {
		stubDir := t.TempDir()
		writeReportingLintStub(t, stubDir, version, 0)

		out, err := runLintTarget(t, root, stubDir, moduleDir, "PARALLEL_GATE_CPU_LIMIT="+strconv.Itoa(cores))
		if err != nil {
			t.Fatalf("the lint target failed a stub that exited 0 at PARALLEL_GATE_CPU_LIMIT=%d: %v\n%s",
				cores, err, out)
		}

		budget := parseLintBudget(t, out)
		budgets[cores] = budget["LINT_TIMEOUT"]

		// The budget the line names has to be the one this run's linter was
		// handed: a uniform number printed beside a module that got another
		// number would satisfy the comparison below without anything being
		// uniform about the run.
		if got := stubReport(t, out)["timeout"]; got != budget["LINT_TIMEOUT"] {
			t.Errorf("at PARALLEL_GATE_CPU_LIMIT=%d the target printed LINT_TIMEOUT=%s but handed the "+
				"linter --timeout %s", cores, budget["LINT_TIMEOUT"], got)
		}
	}

	first := quotas[0]
	for _, cores := range quotas[1:] {
		if budgets[cores] != budgets[first] {
			t.Errorf("a %d-core environment resolved LINT_TIMEOUT=%s and a %d-core one resolved %s: the "+
				"lint budget is decided by the pod's CPU quota, so one commit's lint is handed two "+
				"deadlines and a red produced under the shorter one cannot be told from a lint that "+
				"found something (see the Makefile's LINT_TIMEOUT comment for the trade this makes)",
				first, budgets[first], cores, budgets[cores])
		}
	}
}

// TestLintTargetRepeatsTheBudgetOnItsFailurePath pins Deliverable B: the red
// itself carries the budget, adjacent to the aggregated "lint failed in:" line
// rather than a hundred buffered module lines above it.
func TestLintTargetRepeatsTheBudgetOnItsFailurePath(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	pin, err := os.ReadFile(filepath.Join(root, "GOLANGCI_LINT_VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	version := strings.TrimPrefix(strings.TrimSpace(string(pin)), "v")

	stubDir := t.TempDir()
	writeReportingLintStub(t, stubDir, version, 1)

	out, err := runLintTarget(t, root, stubDir, t.TempDir(), "PARALLEL_GATE_CPU_LIMIT=22")
	if err == nil {
		t.Fatalf("the lint target passed a stub that reported findings:\n%s", out)
	}
	if !strings.Contains(out, "lint failed in:") {
		t.Fatalf("the lint target failed without naming the failing module:\n%s", out)
	}

	// Two budget lines: one before the fan-out, one beside the failure. Both
	// have to name the same numbers -- a failure-path line that disagreed
	// with the run it describes would be its own defect.
	lines := budgetLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected the budget twice (before the fan-out and beside the failure line), got %d:\n%s",
			len(lines), out)
	}
	before, atFailure := parseBudgetLine(t, lines[0], out), parseBudgetLine(t, lines[1], out)
	for field, value := range before {
		if atFailure[field] != value {
			t.Errorf("the budget printed before the fan-out gives %s=%s but the one printed beside the "+
				"failure gives %s=%s: they describe one run and must agree",
				field, value, field, atFailure[field])
		}
	}

	// Adjacent, not merely present: the line that reds the gate and the budget
	// that explains it have to be readable together.
	failureAt := strings.Index(out, "lint failed in:")
	if second := strings.Index(out[failureAt:], lintBudgetMarker); second < 0 {
		t.Errorf("the failure line is not followed by the budget it ran under:\n%s", out)
	} else if gap := strings.Count(out[failureAt:failureAt+second], "\n"); gap > 2 {
		t.Errorf("the budget line sits %d lines below the failure it explains, so a reader still has to "+
			"join them:\n%s", gap, out)
	}
}

const lintBudgetMarker = ">> lint budget: "

var (
	lintBudgetField = regexp.MustCompile(`(LINT_TIMEOUT|LINT_PARALLELISM|LINT_GOMAXPROCS)=([^,\s]+)`)
	lintBudgetQuota = regexp.MustCompile(`cpu quota of (\d+)`)
	stubReportField = regexp.MustCompile(`stub: timeout=(\S+) gomaxprocs=(\S+)`)
)

// budgetLines returns every budget line in the target's output, in order.
func budgetLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if at := strings.Index(line, lintBudgetMarker); at >= 0 {
			lines = append(lines, strings.TrimSpace(line[at:]))
		}
	}
	return lines
}

// parseBudgetLine reads the named values out of one budget line, plus the cpu
// quota it says they were derived from.
func parseBudgetLine(t testing.TB, line, out string) map[string]string {
	t.Helper()
	budget := map[string]string{}
	for _, m := range lintBudgetField.FindAllStringSubmatch(line, -1) {
		budget[m[1]] = m[2]
	}
	for _, want := range []string{"LINT_TIMEOUT", "LINT_PARALLELISM", "LINT_GOMAXPROCS"} {
		if budget[want] == "" {
			t.Errorf("the budget line %q does not name %s:\n%s", line, want, out)
		}
	}

	quota := lintBudgetQuota.FindStringSubmatch(line)
	if quota == nil {
		t.Fatalf("the budget line %q does not name the cpu quota it resolved from, which is the number "+
			"that differs between the pods this line exists to tell apart:\n%s", line, out)
	}
	budget["cpu"] = quota[1]
	return budget
}

// parseLintBudget reads the budget the target printed before its fan-out.
func parseLintBudget(t testing.TB, out string) map[string]string {
	t.Helper()
	lines := budgetLines(out)
	if len(lines) == 0 {
		t.Fatalf("the lint target printed no %q line, so a reader of this gate's log cannot tell what "+
			"budget the lint ran under -- which is indistinguishable from a lint that found something:\n%s",
			lintBudgetMarker, out)
	}
	return parseBudgetLine(t, lines[0], out)
}

// stubReport reads back what the stub linter was actually invoked with.
func stubReport(t testing.TB, out string) map[string]string {
	t.Helper()
	m := stubReportField.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("the stub linter never reported the --timeout and GOMAXPROCS it was run with:\n%s", out)
	}
	return map[string]string{"timeout": m[1], "gomaxprocs": m[2]}
}

// writeReportingLintStub writes a golangci-lint stand-in that answers the
// recipe's version check, then echoes the --timeout it was handed and the
// GOMAXPROCS it inherited before emitting one canned report. Those two values
// are what the assertions above compare the printed budget against, so the
// test reads the real invocation rather than re-deriving what it should be.
func writeReportingLintStub(t testing.TB, dir, version string, status int) {
	t.Helper()
	body := "#!/bin/sh\n" +
		"if [ \"${1:-}\" = \"--version\" ]; then\n" +
		"  printf 'golangci-lint has version " + version + " built with go1.26.0\\n'\n" +
		"  exit 0\n" +
		"fi\n" +
		"timeout_value=none\n" +
		"prev=\n" +
		"for arg in \"$@\"; do\n" +
		"  [ \"$prev\" = \"--timeout\" ] && timeout_value=\"$arg\"\n" +
		"  prev=\"$arg\"\n" +
		"done\n" +
		"printf 'stub: timeout=%s gomaxprocs=%s\\n' \"$timeout_value\" \"${GOMAXPROCS:-unset}\"\n" +
		"cat <<'GOLANGCI_LINT_REPORT'\n" +
		"0 issues.\n" +
		"GOLANGCI_LINT_REPORT\n" +
		"exit " + strconv.Itoa(status) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "golangci-lint"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
