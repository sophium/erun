package eruncommon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The READY rung's build-and-record step is a shell block inside the erun-merge
// skill, and a driver running that skill executes that block and nothing else:
// the prose around it is read by a person, not by the shell. So whatever the
// block does on a build failure is what the review's status actually becomes --
// a remedy written only as a sentence below the block is a remedy a
// non-interactive driver never reaches.
//
// The block builds once and records that one result: the version the build
// minted on success, or --failed on failure. A failure is a verdict about the
// change, so the block must not retry it into a green record.
//
// These tests exercise the block rather than reading it, because the property
// under test is control flow: which failure routes to which record call. A
// structural assertion on the block's text cannot tell "records the failure"
// from "retries until green" without becoming a parser for the
// shell it is trying to check.
//
// The block is run verbatim, with `erun`, `jq` and `git` stubbed ahead of it on
// PATH, so the harness owes nothing to a live platform, a docker daemon, or a
// git checkout -- it runs the same way in the erun-devops image test stage,
// which has no .git.

const (
	readyRungBuildJSON = "/tmp/erun-merge-build.json"
	readyRungBuildErr  = "/tmp/erun-merge-build.err"

	// The commit the stubbed `git rev-parse HEAD` reports. Only 40 lowercase
	// hex characters matter here; nothing resolves it.
	readyRungCommit = "4f3c1a9d2b8e5f6a7c0d1e2f3a4b5c6d7e8f9012"

	// What the failure path's `--dry-run` version mint returns. Distinct from
	// every scripted build outcome so a test can tell which one was recorded.
	readyRungDryRunVersion = "9.9.9-dry"
)

// The two things a stubbed `erun build` can do. `ok:` mints the version that
// follows it; `red` prints the shape of an ordinary build red -- a docker
// failure and a failing check target -- and fails.
const (
	readyRungOutcomeOK         = "ok:"
	readyRungOutcomeRealRed    = "red"
	readyRungNoScriptedOutcome = "stub erun: no outcome scripted for this build"
)

// readyRungStubErun decides what each `erun build` does from the order it is
// called in. The block's builds are sequential by construction, so the call
// count is the whole of the state it needs.
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

// readyRungStubJQ is `jq -r .version <file>` for the compact JSON the stubbed
// erun writes. It reads a file argument when the block supplies one (the JSON
// the build wrote) and stdin when it does not (the piped --dry-run result).
const readyRungStubJQ = `#!/bin/sh
extract='s/.*"version":"\([^"]*\)".*/\1/p'
if [ -n "$3" ]; then
  sed -n "$extract" "$3"
else
  sed -n "$extract"
fi
`

// readyRungStubGit answers the block's only git call.
const readyRungStubGit = `#!/bin/sh
printf '%s\n' "` + readyRungCommit + `"
`

// readyRungRecordBlock returns the rung's build-and-record block: the fenced
// shell block that actually invokes `erun review record-build` against the
// review id rung 4 resolved, as opposed to the pre-flight handoff text, which
// only names the command inside its heredoc. Keyed on the invocation rather
// than on the block's position so that a reordering of the skill cannot point
// this harness at prose.
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

// readyRungRun is what the rung block did: the argv of every `erun` invocation
// it made, in order, and what it wrote to its own stderr. The block sends each
// build's own stdout to the JSON file it reads a version from, so its stderr is
// the operator's only view of the builds and is worth asserting on.
type readyRungRun struct {
	calls  []string
	stderr string
}

// readyRungFixture is everything the stubbed block reads: the bin directory
// that goes ahead of PATH, the log its `erun` calls are appended to, and the
// files the stubbed erun takes its scripted outcomes from.
type readyRungFixture struct {
	binDir       string
	logPath      string
	counterPath  string
	outcomesPath string
}

// newReadyRungFixture lays the stubs down in a fresh temp dir, with each
// `erun build` in turn behaving as the given outcome says.
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

// clearReadyRungBuildFiles clears the two paths the block writes for itself and
// schedules their removal. The block names them as absolute /tmp paths, so the
// harness cannot redirect them into its temp dir without editing the text it
// means to run; a stale file from an earlier case must not decide this one's
// outcome, and neither may be left behind.
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

// runReadyRungFlow runs the skill's READY-rung shell block -- the one that
// records the build against the review -- under the stubs, and reports what it
// did.
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
		// The block's only input from its caller: rungs 1-4 established it.
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

// erunCallsWithPrefix returns the logged invocations whose argv starts with
// prefix, so an assertion can read as "which builds ran, in order".
func erunCallsWithPrefix(calls []string, prefix string) []string {
	var out []string
	for _, call := range calls {
		if strings.HasPrefix(call, prefix) {
			out = append(out, call)
		}
	}
	return out
}

// realBuildCalls returns the logged `erun build` invocations that actually
// build. `--dry-run` mints a version without building anything, so it is the
// failure path's fallback for a version to record rather than a build attempt,
// and counting it as one would read a single failed build as two tries.
func realBuildCalls(calls []string) []string {
	var out []string
	for _, call := range erunCallsWithPrefix(calls, "build ") {
		if !strings.Contains(call, "--dry-run") {
			out = append(out, call)
		}
	}
	return out
}

// recordedBuild returns the block's single `erun review record-build` call.
// Failing on zero or several is the point: every case below is a claim about
// which one record call the flow reached, and a block that recorded nothing, or
// twice, would satisfy the rest of the assertions vacuously.
func recordedBuild(t *testing.T, calls []string) string {
	t.Helper()
	records := erunCallsWithPrefix(calls, "review record-build ")
	if len(records) != 1 {
		t.Fatalf("expected the rung to record the build exactly once, got %d: %v", len(records), records)
	}
	return records[0]
}

// TestMergeSkillReadyRungRecordsTheVersionTheBuildMinted holds the success
// path: one plain build, recorded with the version it minted.
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

// TestMergeSkillReadyRungRecordsAFailedBuildFailedWithoutRetrying holds the
// failure path: a compile error or a red test stage is a verdict about the
// change, recorded FAILED with a version, and never retried into a green
// record.
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
