package mergequeue

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The READY rung's shell block is run verbatim with erun, jq and git stubbed on PATH:
// control flow on failure is what the review's status becomes, and must not retry into green.

const (
	readyRungBuildJSON = "/tmp/erun-merge-build.json"
	readyRungBuildErr  = "/tmp/erun-merge-build.err"

	readyRungCommit = "4f3c1a9d2b8e5f6a7c0d1e2f3a4b5c6d7e8f9012"

	// Distinct from every scripted build outcome so a test can tell which was recorded.
	readyRungDryRunVersion = "9.9.9-dry"
)

// `ok:` mints the version that follows; `red` prints a build failure and fails.
const (
	readyRungOutcomeOK         = "ok:"
	readyRungOutcomeRealRed    = "red"
	readyRungNoScriptedOutcome = "stub erun: no outcome scripted for this build"
)

const readyRungStubErun = `#!/bin/sh
printf '%s\n' "$*" >> "${ERUN_STUB_LOG}"
case "$1" in
  review) exit 0 ;;
  build) ;;
  *) exit 1 ;;
esac
case " $* " in
  *" --dry-run "*) printf '{"version":"` + readyRungDryRunVersion + `"}\n'; exit 0 ;;
esac
counter=$(cat "${ERUN_STUB_COUNTER}")
printf '%s\n' "$((counter + 1))" > "${ERUN_STUB_COUNTER}"
outcome=$(sed -n "$((counter + 1))p" "${ERUN_STUB_OUTCOMES}")
case "${outcome}" in
  ` + readyRungOutcomeOK + `*) printf '{"version":"%s"}\n' "${outcome#` + readyRungOutcomeOK + `}" ;;
  red) printf '%s\n' 'ERROR: failed to solve: process "/bin/sh -c make check" did not complete successfully: exit code: 2' 'make: *** [Makefile:1060: check] Error 2' >&2; exit 1 ;;
  *) printf '%s\n' "${ERUN_STUB_NO_OUTCOME}" >&2; exit 1 ;;
esac
`

// readyRungStubJQ reads a file argument if given, else stdin.
const readyRungStubJQ = `#!/bin/sh
extract='s/.*"version":"\([^"]*\)".*/\1/p'
if [ -n "$3" ]; then
  sed -n "$extract" "$3"
else
  sed -n "$extract"
fi
`

const readyRungStubGit = `#!/bin/sh
printf '%s\n' "` + readyRungCommit + `"
`

// readyRungRecordBlock finds the block by its record-build invocation, not its position.
func readyRungRecordBlock(t *testing.T) string {
	t.Helper()
	var matched []string
	for _, block := range mergeReadyRungBlocks(t, mergeReadyRungSkillPaths[0]) {
		if strings.Contains(block, `erun review record-build "${review_id}"`) {
			matched = append(matched, block)
		}
	}
	if len(matched) != 1 {
		t.Fatalf("expected exactly one erun-merge shell block recording a build against a resolved ${review_id}, found %d; this harness drives one rung", len(matched))
	}
	return matched[0]
}

type readyRungRun struct {
	calls  []string
	stderr string
}

type readyRungFixture struct {
	binDir       string
	logPath      string
	counterPath  string
	outcomesPath string
}

func newReadyRungFixture(t *testing.T, outcomes ...string) readyRungFixture {
	t.Helper()
	dir := t.TempDir()
	fixture := readyRungFixture{
		binDir:       filepath.Join(dir, "bin"),
		logPath:      filepath.Join(dir, "calls.log"),
		counterPath:  filepath.Join(dir, "counter"),
		outcomesPath: filepath.Join(dir, "outcomes"),
	}
	if err := os.MkdirAll(fixture.binDir, 0o755); err != nil {
		t.Fatalf("create stub bin dir: %v", err)
	}
	writeReadyRungStub(t, fixture.binDir, "erun", readyRungStubErun)
	writeReadyRungStub(t, fixture.binDir, "jq", readyRungStubJQ)
	writeReadyRungStub(t, fixture.binDir, "git", readyRungStubGit)

	if err := os.WriteFile(fixture.counterPath, []byte("0\n"), 0o644); err != nil {
		t.Fatalf("seed the build counter: %v", err)
	}
	script := strings.Join(outcomes, "\n") + "\n"
	if err := os.WriteFile(fixture.outcomesPath, []byte(script), 0o644); err != nil {
		t.Fatalf("write the build outcomes: %v", err)
	}
	return fixture
}

// The block writes absolute /tmp paths the harness cannot redirect, so clear them per case.
func clearReadyRungBuildFiles(t *testing.T) {
	t.Helper()
	for _, path := range []string{readyRungBuildJSON, readyRungBuildErr} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatalf("clear %s: %v", path, err)
		}
	}
	t.Cleanup(func() {
		for _, path := range []string{readyRungBuildJSON, readyRungBuildErr} {
			_ = os.Remove(path)
		}
	})
}

func runReadyRungFlow(t *testing.T, outcomes ...string) readyRungRun {
	t.Helper()

	blockPath := filepath.Join(t.TempDir(), "rung.sh")
	if err := os.WriteFile(blockPath, []byte(readyRungRecordBlock(t)), 0o644); err != nil {
		t.Fatalf("write the rung block: %v", err)
	}
	fixture := newReadyRungFixture(t, outcomes...)
	clearReadyRungBuildFiles(t)

	var stdout, stderr strings.Builder
	cmd := exec.Command("sh", blockPath)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(),
		"PATH="+fixture.binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"ERUN_STUB_LOG="+fixture.logPath,
		"ERUN_STUB_COUNTER="+fixture.counterPath,
		"ERUN_STUB_OUTCOMES="+fixture.outcomesPath,
		"ERUN_STUB_NO_OUTCOME="+readyRungNoScriptedOutcome,
		"review_id=01J0READYRUNGREVIEW0000000",
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("the rung block failed to run under the stubs: %v\nstderr:\n%s", err, stderr.String())
	}

	calls, readErr := os.ReadFile(fixture.logPath)
	if readErr != nil {
		t.Fatalf("read the erun call log: %v", readErr)
	}
	return readyRungRun{
		calls:  strings.Split(strings.TrimSpace(string(calls)), "\n"),
		stderr: stderr.String(),
	}
}

func writeReadyRungStub(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatalf("write the %s stub: %v", name, err)
	}
}

func erunCallsWithPrefix(calls []string, prefix string) []string {
	var out []string
	for _, call := range calls {
		if strings.HasPrefix(call, prefix) {
			out = append(out, call)
		}
	}
	return out
}

// realBuildCalls excludes `--dry-run`, which mints a version without building.
func realBuildCalls(calls []string) []string {
	var out []string
	for _, call := range erunCallsWithPrefix(calls, "build ") {
		if !strings.Contains(call, "--dry-run") {
			out = append(out, call)
		}
	}
	return out
}

// recordedBuild fails on zero or several calls, which would satisfy the rest vacuously.
func recordedBuild(t *testing.T, calls []string) string {
	t.Helper()
	records := erunCallsWithPrefix(calls, "review record-build ")
	if len(records) != 1 {
		t.Fatalf("expected the rung to record the build exactly once, got %d: %v", len(records), records)
	}
	return records[0]
}

func TestMergeSkillReadyRungRecordsTheVersionTheBuildMinted(t *testing.T) {
	calls := runReadyRungFlow(t, readyRungOutcomeOK+"1.0.0-ready").calls

	builds := realBuildCalls(calls)
	if len(builds) != 1 {
		t.Fatalf("expected the rung to build exactly once, got %d: %v", len(builds), builds)
	}
	if strings.Contains(builds[0], "--gate") {
		t.Errorf("expected a plain build, got %q", builds[0])
	}

	record := recordedBuild(t, calls)
	if strings.Contains(record, "--failed") {
		t.Errorf("expected a successful build to be recorded as a success, got %q", record)
	}
	if !strings.Contains(record, "--version 1.0.0-ready") {
		t.Errorf("expected the recorded version to be the one the build minted, got %q", record)
	}
	if strings.Contains(record, "--gate") {
		t.Errorf("expected a plain recorded build, got %q", record)
	}
}

func TestMergeSkillReadyRungRecordsAFailedBuildFailedWithoutRetrying(t *testing.T) {
	calls := runReadyRungFlow(t, readyRungOutcomeRealRed).calls

	builds := realBuildCalls(calls)
	if len(builds) != 1 {
		t.Fatalf("expected a build failure to run no second build, got %d builds: %v", len(builds), builds)
	}

	record := recordedBuild(t, calls)
	if !strings.Contains(record, "--failed") {
		t.Errorf("expected a failed build to record FAILED, got %q", record)
	}
	if !strings.Contains(record, "--version ") {
		t.Errorf("expected the failed build to be recorded with a version, got %q", record)
	}
	if strings.Contains(record, "--gate") {
		t.Errorf("expected a plain recorded build, got %q", record)
	}
}
