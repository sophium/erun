package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// zzReinvocationStubClaude puts a stub `claude` on PATH — the name both the
// original argv and the resume argv are built with, so the stub stands in for
// the real tool on every turn, exactly as the integration harness's own stub
// does. It reports a session id on every turn, and appends its own argv to
// capture every time it is invoked as a *resumed* turn. capture is how a test
// reads back the prompt the supervisor actually handed the resumed turn,
// rather than the string it meant to hand it.
func zzReinvocationStubClaude(t *testing.T, capture string) {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
for arg in "$@"; do
	if [ "$arg" = "--resume" ]; then
		printf '%%s\n' "$*" >> %q
		break
	fi
done
printf '{"type":"system","subtype":"init","session_id":"11111111-1111-1111-1111-111111111111"}\n'
printf '{"type":"result","subtype":"success","is_error":false,"num_turns":1,"result":"done"}\n'
`, capture)
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub claude: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// zzRunLaneWithFailedProbe runs a real agent-job supervisor for a lane whose
// own probe sub-job has already finished, failed, and been recorded — the
// state every reproduction lane ends in, and the state that makes the
// supervisor resume the lane's session. It returns the lane's final record and
// the argv the resumed turn was actually run with.
func zzRunLaneWithFailedProbe(t *testing.T, task string) (EnvironmentJob, string) {
	t.Helper()
	isolateActivityCache(t)

	const tenant = "reinvocation-task"
	const environment = "lane"
	const laneID = "lane"

	dir, err := environmentJobDir(tenant, environment)
	if err != nil {
		t.Fatalf("environmentJobDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}

	// The lane's own probe: a job it started, that reds on purpose, and that
	// had already finished by the time the lane's own process ended. Nothing
	// about the record distinguishes "red on purpose" from any other failure —
	// that is the whole point, and why the resumption prompt is the only place
	// the lane's finding can be protected.
	probeCode := 1
	if err := writeEnvironmentJob(dir, EnvironmentJob{
		ID:             "probe",
		Name:           "probe",
		State:          EnvironmentJobStateExited,
		StartedAt:      time.Now(),
		EndedAt:        time.Now(),
		ExitCode:       &probeCode,
		StartedByJobID: laneID,
		LeaseID:        environmentJobLeaseID("probe"),
	}); err != nil {
		t.Fatalf("seed the lane's own failed probe: %v", err)
	}

	capture := filepath.Join(t.TempDir(), "resumed-argv")
	zzReinvocationStubClaude(t, capture)

	// The lane's own argv, built by the same constructor production uses, and
	// recorded verbatim — exactly the shape a real record keeps.
	command, err := AgentJobCommand("claude", task)
	if err != nil {
		t.Fatalf("AgentJobCommand: %v", err)
	}

	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          laneID,
		Name:        laneID,
		Agent:       "claude",
		Command:     command,
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}

	lane, err := LoadEnvironmentJob(tenant, environment, laneID, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	captured, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("the lane was never resumed, so nothing captured its resumed argv: %v (lane: %+v)", err, lane)
	}
	return lane, string(captured)
}

// The reproduction of the reported loss. A lane is dispatched with a brief that
// asks for a failing probe, which is what every reproduction is: the probe reds on
// purpose, the supervisor resumes the lane to account for it, and the lane
// answers *that* — so the finding the brief asked for is never delivered, and
// the job still reads as `exited 0`.
//
// The decisive part is what the resumed turn is handed. Before this change the
// resumption carried only the failure, so the resumed turn's final message —
// the only channel a one-shot run's finding has — was an explanation of the
// lane's own scratch probe. Here the argv the supervisor actually runs is read
// back and must still carry the task the run was originally given, so the
// final message has somewhere to put the answer.
func TestReinvocationResumesWithTheTaskTheRunWasOriginallyGiven(t *testing.T) {
	const task = "verify the titlebar budget mechanism and report the verdict"

	lane, resumedArgv := zzRunLaneWithFailedProbe(t, task)

	// The resumption is still the bounded recovery it was: the same job, the
	// same session, the same failure named, the same bound.
	if lane.ReinvocationCount != resolveEnvironmentJobMaxReinvocations() {
		t.Fatalf("ReinvocationCount = %d, want the bound %d", lane.ReinvocationCount, resolveEnvironmentJobMaxReinvocations())
	}
	if !strings.Contains(resumedArgv, "11111111-1111-1111-1111-111111111111") {
		t.Fatalf("the resumed turn did not carry the captured session id: %q", resumedArgv)
	}
	if !strings.Contains(resumedArgv, "probe") {
		t.Fatalf("the resumed turn no longer names the job that failed: %q", resumedArgv)
	}

	if !strings.Contains(resumedArgv, task) {
		t.Fatalf("the resumed turn was handed only the outcome, so the lane's finding had nowhere to go:\n%s", resumedArgv)
	}
	if !strings.Contains(resumedArgv, "must still deliver that answer") {
		t.Fatalf("the resumed turn restates the task without saying its final message must still answer it:\n%s", resumedArgv)
	}
}

// The other half of the contract, asserted directly because the reproduction
// above can only exercise the recoverable case: a job whose agent argv is not
// recoverable gets today's prompt verbatim, with nothing invented in place of
// a task nobody recorded.
func TestReinvocationPromptInventsNoTaskItCannotRecover(t *testing.T) {
	job := EnvironmentJob{
		Kind:      EnvironmentJobKindAgent,
		AgentTool: "claude",
		Command:   []string{"claude", "--output-format", "stream-json", "-p", "task"},
		Progress:  &AgentJobProgress{SessionID: "session"},
	}
	got := buildEnvironmentJobReinvocationPrompt(job, "the job this job started (gate) did not succeed")
	if strings.Contains(got, "The task you were given was") {
		t.Fatalf("a prompt was recovered from an argv that is not AgentJobCommand's output: %q", got)
	}
	if !strings.Contains(got, "the job this job started (gate) did not succeed") {
		t.Fatalf("the outcome is no longer named: %q", got)
	}

	commandJob := EnvironmentJob{
		Kind:     EnvironmentJobKindCommand,
		Command:  []string{"make", "check"},
		Progress: &AgentJobProgress{SessionID: "session"},
	}
	if got := buildEnvironmentJobReinvocationPrompt(commandJob, "outcome"); strings.Contains(got, "The task you were given was") {
		t.Fatalf("a command job's argv was read back as an agent task: %q", got)
	}
}

// AgentJobCommandPrompt is the inverse of AgentJobCommand or it is a way to
// hand a resumed turn a fragment of some unrelated argv, so the round trip is
// pinned for every tool and the refusals are pinned for the shapes it must not
// guess at.
func TestAgentJobCommandPromptIsAgentJobCommandsInverse(t *testing.T) {
	for _, tool := range AgentJobTools {
		const task = "check the budget mechanism, and say what it does with an unspent window"
		command, err := AgentJobCommand(tool, task)
		if err != nil {
			t.Fatalf("AgentJobCommand(%q): %v", tool, err)
		}
		if got := AgentJobCommandPrompt(tool, command); got != task {
			t.Fatalf("AgentJobCommandPrompt(%q) = %q, want the prompt it was built from", tool, got)
		}
	}

	for name, tc := range map[string]struct {
		tool    string
		command []string
	}{
		"a command job's argv":      {"", []string{"make", "check"}},
		"an unknown tool":           {"aider", []string{"aider", "-p", "task"}},
		"a claude argv without -p":  {"claude", []string{"claude", "task", "--output-format", "stream-json"}},
		"a claude argv unlike ours": {"claude", []string{"claude", "--output-format", "stream-json", "-p", "task"}},
		"a truncated claude argv":   {"claude", []string{"claude", "-p"}},
		"a truncated codex argv":    {"codex", []string{"codex", "exec", "--json"}},
		"no argv at all":            {"claude", nil},
	} {
		if got := AgentJobCommandPrompt(tc.tool, tc.command); got != "" {
			t.Errorf("%s: AgentJobCommandPrompt = %q, want empty", name, got)
		}
	}
}
