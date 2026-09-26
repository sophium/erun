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
// A build that watched BuildKit replay a Dockerfile's whole test stage exits
// non-zero and is refused deliberately (ErrGateTestStageReplayed), and the
// remedy is another build: the same step with --gate, which executes that stage
// instead of accepting the replay. The block has to take that second step
// itself, and it has to take it only for that one refusal -- a compile error or
// a red `make check` is a verdict about the change, and retrying one under
// --gate would record a green review over a red build.
//
// These tests exercise the block rather than reading it, because the property
// under test is control flow: which failure routes to which record call. A
// structural assertion on the block's text cannot tell "retries on a replay
// refusal" from "retries on everything" without becoming a parser for the
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

	// The detail the block records for a failure that is not a replay refusal.
	readyRungPlainFailureDetail = "erun build failed; see the build log"
)

// The three things a stubbed `erun build` can do. `ok:` mints the version that
// follows it; `replay` prints a real replay refusal and fails; `red` prints the
// shape of an ordinary build red -- a docker failure and a failing check
// target -- and fails.
const (
	readyRungOutcomeOK         = "ok:"
	readyRungOutcomeReplay     = "replay"
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
  replay) printf '%s\n' "${ERUN_STUB_REPLAY_MESSAGE}" >&2; exit 1 ;;
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
		// The replay refusal's real text, not a paraphrase of it: whether the
		// block recognises the refusal is exactly what this harness measures,
		// and a hand-written stand-in could agree with a matcher the real
		// message does not.
		"ERUN_STUB_REPLAY_MESSAGE="+newGateTestStageReplayedError([]string{"erun-devops"}).Error(),
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

// TestMergeSkillReadyRungReachesItsOwnReplayRemedy is the reproduction of the
// reported defect. The block's remedy for a replayed test stage was a sentence
// underneath it -- re-run the step with --gate -- and the failure branch
// recorded FAILED for every non-zero build, so a driver that ran the block and
// nothing else left the review at FAILED and never reached the escape the file
// documents. On the pre-fix block this case records the --dry-run version with
// --failed and runs no --gate build at all.
func TestMergeSkillReadyRungReachesItsOwnReplayRemedy(t *testing.T) {
	run := runReadyRungFlow(t, readyRungOutcomeReplay, readyRungOutcomeOK+"1.0.0-gate")
	calls := run.calls

	builds := realBuildCalls(calls)
	if len(builds) != 2 {
		t.Fatalf("expected the rung to build twice -- the plain build and the replay remedy -- got %d: %v", len(builds), builds)
	}
	if builds[0] != "build --output json" {
		t.Errorf("expected the rung to start with the plain build, got %q", builds[0])
	}
	if !strings.Contains(builds[1], "--gate") {
		t.Fatalf("expected the second build to be the --gate re-run that executes the replayed stage, got %q", builds[1])
	}

	record := recordedBuild(t, calls)
	if strings.Contains(record, "--failed") {
		t.Errorf("expected a replay refusal to be recorded as the re-run's result, not as a FAILED review, got %q", record)
	}
	if !strings.Contains(record, "--version 1.0.0-gate") {
		t.Errorf("expected the recorded version to be the one the --gate re-run minted, got %q", record)
	}
	// The build's stderr is captured to classify the failure, so the block has
	// to put it back: a refusal the operator never sees is its own defect.
	if !strings.Contains(run.stderr, ErrGateTestStageReplayed.Error()) {
		t.Errorf("expected the refusal to be reported to the operator, got stderr:\n%s", run.stderr)
	}
}

// TestMergeSkillReadyRungKeepsAnOrdinaryBuildFailureFailed is the other half of
// the property, and the one that makes the fix safe: the re-run is keyed on the
// replay refusal itself, not on the build having failed. A compile error, a red
// `make check`, or a missing base image is a verdict about the change, and a
// --gate re-run over one would record a green review over a red build.
func TestMergeSkillReadyRungKeepsAnOrdinaryBuildFailureFailed(t *testing.T) {
	calls := runReadyRungFlow(t, readyRungOutcomeRealRed).calls

	builds := realBuildCalls(calls)
	if len(builds) != 1 {
		t.Fatalf("expected an ordinary build failure to run no remedy build, got %d builds: %v", len(builds), builds)
	}
	if builds[0] != "build --output json" {
		t.Errorf("expected the rung's only build to be the plain one, got %q", builds[0])
	}

	record := recordedBuild(t, calls)
	if !strings.Contains(record, "--failed") {
		t.Errorf("expected a build failure that is not a replay refusal to record FAILED, got %q", record)
	}
	if !strings.Contains(record, "--failure-detail "+readyRungPlainFailureDetail) {
		t.Errorf("expected the ordinary failure to keep its own detail, got %q", record)
	}
}

// A replay refusal whose --gate re-run then fails is a real red: the re-run
// threw away the layer-cache replay and ran make check, and make check did not
// pass. That must land FAILED too, and must say which build was red -- the
// review is not left green by a remedy that itself failed.
func TestMergeSkillReadyRungDoesNotMaskARedGateRerun(t *testing.T) {
	calls := runReadyRungFlow(t, readyRungOutcomeReplay, readyRungOutcomeRealRed).calls

	builds := realBuildCalls(calls)
	if len(builds) != 2 || !strings.Contains(builds[1], "--gate") {
		t.Fatalf("expected the replay refusal to spend its one re-run on a --gate build, got %v", builds)
	}

	record := recordedBuild(t, calls)
	if !strings.Contains(record, "--failed") {
		t.Errorf("expected a red --gate re-run to record FAILED, got %q", record)
	}
	if strings.Contains(record, "--version 1.0.0-gate") {
		t.Errorf("expected a failed re-run to record no minted version as a success, got %q", record)
	}
	if !strings.Contains(record, "--gate re-run") {
		t.Errorf("expected the failure detail to name the re-run that was red rather than the plain build, got %q", record)
	}
}

// The match is the refusal's own sentinel sentence. Binding it here to
// ErrGateTestStageReplayed means the pattern the block greps for and the error
// erun-common actually returns cannot drift apart silently: rewording the
// sentinel fails this test instead of quietly disarming the remedy in the
// skill, which is the failure mode the remedy exists to prevent and the one no
// live build in a test could catch.
func TestMergeSkillReadyRungMatchesTheReplayRefusalItClaimsTo(t *testing.T) {
	block := readyRungRecordBlock(t)
	if !strings.Contains(block, ErrGateTestStageReplayed.Error()) {
		t.Errorf("the READY rung does not match %q, the text ErrGateTestStageReplayed carries; its replay remedy is keyed on something else", ErrGateTestStageReplayed.Error())
	}
	if !strings.Contains(block, "--dry-run") {
		t.Errorf("the READY rung's failure path no longer resolves a version, so a failed build has nothing to record")
	}
}
