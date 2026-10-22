package integration

// The lint target's verdict has to come from golangci-lint's own report, not
// from golangci-lint's exit status, because the two disagree in exactly the
// case that reds the gate for no finding. golangci-lint prints its whole
// report -- the findings, or "0 issues." -- and only then decides how to exit,
// and that decision consults the run's own --timeout before it consults the
// report: an analysis that finished cleanly is reported as a timeout when the
// deadline expired around it.
//
// The report this pins: check-gate asked for a 900s lint (LINT_TIMEOUT's
// floor, and what the 22-core reference resolves to), erun-backend-api's
// analysis finished and printed "0 issues.", and the deadline error followed
// it -- at 912s, and again at 1296s, against a next-slowest module of 527s.
// The change under that gate touched no Go file. Outside, that is
// indistinguishable from a real lint failure: `erun review record-build
// --gate --failed` ejects a review from the merge queue on it, and a failed
// test stage tags no image, so it costs a full cold gate build too.
//
// Every case below drives the real `lint` recipe -- real make, real fan-out --
// with a stub golangci-lint on PATH replaying one output shape and exit
// status. Driving the recipe rather than the helper is the point: what this
// fixes is the recipe's wiring, and a test that only called the helper would
// not notice that wiring being removed again. None of it reads a clock or
// depends on load, so it fails on any machine, not only on a contended one.

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/harnessexec"
)

func TestLintTargetReadsGolangciLintsReportNotItsDeadline(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	pin, err := os.ReadFile(filepath.Join(root, "GOLANGCI_LINT_VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	version := strings.TrimPrefix(strings.TrimSpace(string(pin)), "v")

	// The module directory only has to exist: the stub answers for it.
	moduleDir := t.TempDir()

	cases := []struct {
		name      string
		report    string
		status    int
		wantGreen bool
		because   string
	}{
		{
			// The reported failure, byte for byte: a complete, clean report
			// followed by the deadline that expired around it.
			name: "a complete clean report and then the deadline error is not a lint failure",
			report: "0 issues.\n" +
				"level=error msg=\"Timeout exceeded: try increasing it by passing --timeout option\"\n",
			status:    4,
			wantGreen: true,
			because:   "the analysis finished and found nothing; only the clock failed, and the gate asks whether the lint found anything",
		},
		{
			name: "findings still fail the target",
			report: "pkg/x.go:12:3: parameter 'a' seems to be unused (revive)\n" +
				"1 issues:\n" +
				"* revive: 1\n",
			status:    1,
			wantGreen: false,
			because:   "a lint that found something has to red the gate; a fix that made the target quiet would be a gate that verifies nothing",
		},
		{
			// The state the fix must not swallow: the deadline masks the exit
			// code golangci-lint would otherwise have chosen for its findings.
			name: "the deadline message does not excuse findings",
			report: "pkg/x.go:12:3: parameter 'a' seems to be unused (revive)\n" +
				"1 issues:\n" +
				"* revive: 1\n" +
				"level=error msg=\"Timeout exceeded: try increasing it by passing --timeout option\"\n",
			status:    4,
			wantGreen: false,
			because:   "the timeout message alone is not what makes a run clean; the report has to say so",
		},
		{
			// The other shape under the same contention, where the lint never
			// produced a report at all: it failed while loading packages. No
			// report is no verdict, and passing it would report a green gate
			// for a module nothing analyzed.
			name: "a run with no report stays red",
			report: "level=error msg=\"Running error: context loading failed: failed to load packages: failed to load with go/packages: context deadline exceeded\"\n" +
				"level=error msg=\"Timeout exceeded: try increasing it by passing --timeout option\"\n",
			status:    4,
			wantGreen: false,
			because:   "there is no report to read, so nothing establishes that the module was analyzed",
		},
		{
			// A clean report line is not on its own enough, either: a run that
			// logged its own analysis error and still printed "0 issues." is
			// reporting a zero it could not stand behind.
			name: "a clean report alongside another logged error stays red",
			report: "0 issues.\n" +
				"level=error msg=\"Running error: failed to analyze\"\n" +
				"level=error msg=\"Timeout exceeded: try increasing it by passing --timeout option\"\n",
			status:    4,
			wantGreen: false,
			because:   "the deadline is the only complaint a clean run may carry",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubDir := t.TempDir()
			writeLintStub(t, stubDir, version, tc.report, tc.status)

			out, err := runLintTarget(t, root, stubDir, moduleDir)
			green := err == nil
			if green == tc.wantGreen {
				if green && !strings.Contains(out, tc.report) {
					t.Errorf("the lint target passed without showing golangci-lint's own output:\n%s\n"+
						"a pass that hides what the run said is indistinguishable from a run that never happened",
						out)
				}
				return
			}
			if tc.wantGreen {
				t.Errorf("the lint target failed a module whose analysis completed with no findings: %v\n"+
					"%s\n%s\n%s", err, strings.Repeat("-", 40), out, strings.Repeat("-", 40))
			}
			t.Errorf("the lint target passed a module that should have failed it (%s):\n%s", tc.because, out)
		})
	}
}

// runLintTarget runs the real `lint` recipe over moduleDir with stubDir first
// on PATH, and returns its combined output and error. It runs make as the
// host resolves it and fails loudly rather than skipping when it is missing:
// the image test stage runs `make check` itself, so make is never absent where
// this is gated.
//
// extraEnv is appended after the inherited environment, so a caller can pin a
// resolved budget (PARALLEL_GATE_CPU_LIMIT) and assert against a known one
// instead of against whatever this machine happens to be.
func runLintTarget(t testing.TB, root, stubDir, moduleDir string, extraEnv ...string) (string, error) {
	t.Helper()
	makeBin, err := osexec.LookPath("make")
	if err != nil {
		t.Fatalf("the integration suite needs \"make\" on the host PATH: %v", err)
	}

	cmd := harnessexec.Command(makeBin, "LINT_MODULES="+moduleDir, "lint")
	cmd.Dir = root
	// StubDir leads so the linter under test is the stub and never a real
	// install; the rest of PATH is the real toolchain the recipe itself needs
	// (sh, bash, tr, grep).
	cmd.Env = append(lintMakeEnv(t), "PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Env = append(cmd.Env, extraEnv...)

	out, err := cmd.CombinedOutput()
	return string(out), err
}

// lintMakeEnv is the environment minus make's own control variables. This
// suite runs from inside `make check`, so MAKEFLAGS arrives carrying the outer
// make's -j and its jobserver file descriptors; a nested make that inherits
// them contends for tokens the outer run is holding while it waits for this
// test to finish.
func lintMakeEnv(t testing.TB) []string {
	t.Helper()
	env := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		switch name, _, _ := strings.Cut(kv, "="); name {
		case "MAKEFLAGS", "MFLAGS", "MAKELEVEL", "MAKE_TERMOUT", "MAKE_TERMERR":
			continue
		}
		env = append(env, kv)
	}
	return env
}

// writeLintStub writes the golangci-lint stand-in: it answers the recipe's
// up-front version check with the pinned version it reads from the checkout,
// and answers every other invocation with one canned report and exit status.
func writeLintStub(t testing.TB, dir, version, report string, status int) {
	t.Helper()
	body := "#!/bin/sh\n" +
		"if [ \"${1:-}\" = \"--version\" ]; then\n" +
		"  printf 'golangci-lint has version " + version + " built with go1.26.0\\n'\n" +
		"  exit 0\n" +
		"fi\n" +
		"cat <<'GOLANGCI_LINT_REPORT'\n" + report + "GOLANGCI_LINT_REPORT\n" +
		"exit " + strconv.Itoa(status) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "golangci-lint"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
