package jobs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared/jobexclusive"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
)

// zzReinvocationStubClaude stubs `claude` on PATH, reporting a session id and
// appending each resumed turn's argv to capture.
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

// zzRunLaneWithFailedProbe runs an agent lane whose probe sub-job already failed,
// returning the lane record and the resumed turn's argv.
func zzRunLaneWithFailedProbe(t *testing.T, task string) (jobstate.EnvironmentJob, string) {
	t.Helper()
	isolateActivityCache(t)

	const tenant = "reinvocation-task"
	const environment = "lane"
	const laneID = "lane"

	dir, err := jobexclusive.EnvironmentJobDir(tenant, environment)
	if err != nil {
		t.Fatalf("environmentJobDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}

	// The probe reds on purpose; its record looks like any other failure.
	probeCode := 1
	if err := writeEnvironmentJob(dir, jobstate.EnvironmentJob{
		ID:             "probe",
		Name:           "probe",
		State:          jobstate.EnvironmentJobStateExited,
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

	command, err := AgentJobCommand("claude", task)
	if err != nil {
		t.Fatalf("AgentJobCommand: %v", err)
	}

	if err := RunEnvironmentJobSupervisor(model.EnvironmentJobSupervisorParams{
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

// A resumed turn must still carry the original task, or its final message
// explains the probe instead of delivering the finding.
func TestReinvocationResumesWithTheTaskTheRunWasOriginallyGiven(t *testing.T) {
	const task = "verify the titlebar budget mechanism and report the verdict"

	lane, resumedArgv := zzRunLaneWithFailedProbe(t, task)

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

// An unrecoverable argv gets the plain prompt, with no invented task.
func TestReinvocationPromptInventsNoTaskItCannotRecover(t *testing.T) {
	job := jobstate.EnvironmentJob{
		Kind:      EnvironmentJobKindAgent,
		AgentTool: "claude",
		Command:   []string{"claude", "--output-format", "stream-json", "-p", "task"},
		Progress:  &jobstate.AgentJobProgress{SessionID: "session"},
	}
	got := buildEnvironmentJobReinvocationPrompt(job, "the job this job started (gate) did not succeed")
	if strings.Contains(got, "The task you were given was") {
		t.Fatalf("a prompt was recovered from an argv that is not AgentJobCommand's output: %q", got)
	}
	if !strings.Contains(got, "the job this job started (gate) did not succeed") {
		t.Fatalf("the outcome is no longer named: %q", got)
	}

	commandJob := jobstate.EnvironmentJob{
		Kind:     EnvironmentJobKindCommand,
		Command:  []string{"make", "check"},
		Progress: &jobstate.AgentJobProgress{SessionID: "session"},
	}
	if got := buildEnvironmentJobReinvocationPrompt(commandJob, "outcome"); strings.Contains(got, "The task you were given was") {
		t.Fatalf("a command job's argv was read back as an agent task: %q", got)
	}
}

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
