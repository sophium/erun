package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testJobAgentStartDryRunPlansTheStreamingInvocation(t *testing.T) {
	// An agent job names a tool and a prompt; erun builds the argv. The plan is
	// where that argv is auditable, and the streaming flags are the whole point:
	// without them the tool prints nothing until it exits, so a multi-hour run
	// would report no output while it is actively editing files.
	//
	// Both tools are stubbed present: the dry-run plan also resolves whether the
	// named tool is available in this environment, which is a real
	// filesystem/PATH check that runs regardless of --dry-run, the same way the
	// job dir check does.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinary(t, stubs, "claude", "")
	fixture.StubBinary(t, stubs, "codex", "")
	envVars := inEnvironment(append(setup.Env(), fixture.StubEnv(stubs, "claude", "codex")...))
	claude := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "sweep", "--agent", "claude", "--dry-run", "--", "fix the failing tests"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if claude.ExitCode != 0 {
		t.Fatalf("exit %d: %s", claude.ExitCode, claude.Combined)
	}
	codex := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "sweep", "--agent", "codex", "--dry-run", "--", "fix the failing tests"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if codex.ExitCode != 0 {
		t.Fatalf("exit %d: %s", codex.ExitCode, codex.Combined)
	}
	golden.Equal(t, "job/agent_start_dry_run_plans_the_streaming_invocation", normalize.Apply(claude.Combined+codex.Combined))
}

func testJobAgentStartRefusesWhenTheToolIsNotAvailable(t *testing.T) {
	// A knowable-before-spend precondition: an agent job naming a tool this
	// environment does not have refuses before it takes a lease or starts a
	// supervisor, rather than accepting the job and failing a minute later on
	// the tool's own (often credential-shaped) error.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "sweep", "--agent", "claude", "--", "fix the failing tests"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode == 0 {
		t.Fatalf("expected a refusal when claude is not installed in this environment, got 0:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, `agent tool "claude" is not available in this environment`) {
		t.Fatalf("the refusal must name the tool and say it is unavailable, got:\n%s", result.Combined)
	}
	if _, err := os.Stat(jobRecordPath(setup, "team", "dev", "sweep")); !os.IsNotExist(err) {
		t.Errorf("a refused start must not register a job, stat err: %v", err)
	}
}

func testJobAgentProgressReportsAnAuthenticationFailureAsTheReason(t *testing.T) {
	// The tool is present but its credentials are stale — a real failure the
	// preflight above cannot catch (the tool is installed). The job's own
	// folded progress already carries the tool's raw auth message; the failed
	// job's reason must say plainly that this looks like a credential problem
	// in the environment, not a bug in the work, rather than leaving the raw
	// vendor string to speak for itself.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryWithScript(t, stubs, "claude",
		`printf '{"type":"result","subtype":"error_during_execution","is_error":true,"num_turns":1,"result":"Failed to authenticate: OAuth session expired and could not be refreshed"}\n'`+"\n"+
			"exit 1")
	envVars := inEnvironment(append(setup.Env(), fixture.StubEnv(stubs, "claude")...))

	start := startJob(t, setup, envVars, "sweep", "--agent", "claude", "--", "fix the failing tests")
	if start.ExitCode != 0 {
		t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
	}
	await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "sweep", "--timeout", "30s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if await.ExitCode != 1 {
		t.Fatalf("await: exit %d: %s", await.ExitCode, await.Combined)
	}
	status := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "sweep", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if status.ExitCode != 0 {
		t.Fatalf("status: exit %d: %s", status.ExitCode, status.Combined)
	}
	var payload struct {
		Reason   string `json:"reason"`
		Progress struct {
			Error string `json:"error"`
		} `json:"progress"`
	}
	if err := json.Unmarshal([]byte(status.Stdout), &payload); err != nil {
		t.Fatalf("parse job status JSON: %v\n%s", err, status.Stdout)
	}
	if !strings.Contains(payload.Progress.Error, "OAuth session expired") {
		t.Fatalf("expected the raw vendor message preserved in progress.error, got %q", payload.Progress.Error)
	}
	if !strings.Contains(payload.Reason, "credentials are missing or stale") {
		t.Fatalf("expected the reason to reframe the failure as a credential problem, got %q", payload.Reason)
	}
}

func testJobAgentProgressReportsAGatewayReasoningRefusalAsTheReason(t *testing.T) {
	// A gateway serving a reasoning model refuses the conversation when it
	// wants the model's own reasoning handed back, and a client can only echo
	// reasoning the gateway returned — so the refusal lands mid-run however
	// far the job already got, and the work it did up to that point is all it
	// has to show. The two events below are captured verbatim from `claude -p
	// --output-format stream-json` on that refusal (the second is the closing
	// envelope erun folds the error from), and the failed job's reason must
	// say what was refused and that the work itself did not fail, rather than
	// reporting a bare exit code under a checkpoint that names nothing.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	refusal := "API Error: 400 The `reasoning_content` in the thinking mode must be passed back to the API."
	events := []string{
		`{"type":"assistant","message":{"model":"<synthetic>","stop_reason":"stop_sequence","content":[{"type":"text","text":"` + refusal + `"}]}}`,
		`{"type":"result","subtype":"success","is_error":true,"result":"` + refusal + `"}`,
	}
	quoted := make([]string, 0, len(events))
	for _, event := range events {
		quoted = append(quoted, "'"+event+"'")
	}
	fixture.StubBinaryWithScript(t, stubs, "claude",
		"printf '%s\\n' "+strings.Join(quoted, " ")+"\nexit 1")
	envVars := inEnvironment(append(setup.Env(), fixture.StubEnv(stubs, "claude")...))

	start := startJob(t, setup, envVars, "sweep", "--agent", "claude", "--", "fix the failing tests")
	if start.ExitCode != 0 {
		t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
	}
	await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "sweep", "--timeout", "30s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if await.ExitCode != 1 {
		t.Fatalf("await: exit %d: %s", await.ExitCode, await.Combined)
	}
	status := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "sweep", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if status.ExitCode != 0 {
		t.Fatalf("status: exit %d: %s", status.ExitCode, status.Combined)
	}
	var payload struct {
		Reason   string `json:"reason"`
		Progress struct {
			Error string `json:"error"`
		} `json:"progress"`
	}
	if err := json.Unmarshal([]byte(status.Stdout), &payload); err != nil {
		t.Fatalf("parse job status JSON: %v\n%s", err, status.Stdout)
	}
	if !strings.Contains(payload.Progress.Error, "reasoning_content") {
		t.Fatalf("expected the gateway's raw refusal preserved in progress.error, got %q", payload.Progress.Error)
	}
	if !strings.Contains(payload.Reason, "reasoning cannot be echoed back through this gateway") {
		t.Fatalf("expected the reason to name the gateway's reasoning round-trip as the cause, got %q", payload.Reason)
	}
	if !strings.Contains(payload.Reason, "rather than the work failing") {
		t.Fatalf("expected the reason to distinguish the refusal from a failed task, got %q", payload.Reason)
	}
}

func testJobAgentStartRefusesAnUnsupportedToolOrACommand(t *testing.T) {
	// A caller that meant a command and a caller that meant an agent must not be
	// silently given the other, and an unsupported tool must name the ones erun
	// can actually stream.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	unsupported := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "sweep", "--agent", "gemini", "--dry-run", "--", "do the thing"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if unsupported.ExitCode == 0 {
		t.Fatalf("expected an unsupported agent tool to fail, got 0:\n%s", unsupported.Combined)
	}
	golden.Equal(t, "job/agent_start_refuses_an_unsupported_tool_or_a_command", normalize.Apply(unsupported.Combined))
}

func testJobAgentProgressIsReadableWhileTheAgentWorks(t *testing.T) {
	// The defect this kind closes: a detached agent run reporting nothing but
	// "running" for its whole life. The stub emits the tool's real stream-json
	// event shapes and then blocks, so the scenario reads mid-run on an
	// observable condition and locks that erun answers what the agent is doing.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	flushed := filepath.Join(setup.Cwd, "flushed")
	release := filepath.Join(setup.Cwd, "release")
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryWithScript(t, stubs, "claude",
		`printf '{"type":"system","subtype":"init","session_id":"s1"}\n'`+"\n"+
			`printf '{"type":"assistant","message":{"id":"msg_1","content":[{"type":"text","text":"Starting the sweep."}]}}\n'`+"\n"+
			`printf '{"type":"assistant","message":{"id":"msg_1","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"erun-common/mcp_client.go"}}]}}\n'`+"\n"+
			"printf '"+jobStubSignal+"' > '"+flushed+"'\n"+
			"while [ ! -f '"+release+"' ]; do sleep 0.05; done\n"+
			`printf '{"type":"assistant","message":{"id":"msg_2","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go test ./..."}}]}}\n'`+"\n"+
			`printf '{"type":"result","subtype":"success","is_error":false,"num_turns":2,"result":"Fixed the client."}\n'`+"\n"+
			"exit 0")
	envVars := inEnvironment(append(setup.Env(), fixture.StubEnv(stubs, "claude")...))

	start := startJob(t, setup, envVars, "sweep", "--agent", "claude", "--", "fix the failing tests")
	if start.ExitCode != 0 {
		t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
	}
	waitForFile(t, flushed, 30*time.Second)
	waitForJobActivity(t, setup, envVars, "sweep", 30*time.Second)

	// job_output already serves the events, and job status answers the activity
	// — the two together are what replace scraping the agent's own transcript.
	midRun := erun.Run(t, []string{"job", "output", "--tenant", "team", "--environment", "dev", "--id", "sweep"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	status := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "sweep"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	// The lease already names the work for the desktop; an agent job renames it
	// to the current activity so "busy" becomes "editing <file>".
	lease := erun.Run(t, []string{"activity", "lease", "list", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})

	if err := os.WriteFile(release, []byte("go\n"), 0o644); err != nil {
		t.Fatalf("release the stub: %v", err)
	}
	await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "sweep", "--timeout", "30s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if await.ExitCode != 0 {
		t.Fatalf("await: exit %d: %s", await.ExitCode, await.Combined)
	}
	finished := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "sweep"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "job/agent_progress_is_readable_while_the_agent_works", normalize.Apply(
		midRun.Combined+status.Combined+lease.Combined+finished.Combined))
}

func testJobAgentProgressIsTheSameShapeForEveryTool(t *testing.T) {
	// Normalizing inside erun is what keeps the progress contract stable across
	// AI tools. Codex reports work as thread items rather than tool calls, and
	// the caller still reads one activity line and one pair of counts.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryWithScript(t, stubs, "codex",
		`printf '{"type":"thread.started","thread_id":"th_1"}\n'`+"\n"+
			`printf '{"type":"turn.started"}\n'`+"\n"+
			`printf '{"type":"item.started","item":{"id":"i1","type":"file_change","changes":[{"path":"erun-common/mcp_client.go","kind":"update"}]}}\n'`+"\n"+
			`printf '{"type":"item.completed","item":{"id":"i1","type":"file_change","changes":[{"path":"erun-common/mcp_client.go","kind":"update"}]}}\n'`+"\n"+
			`printf '{"type":"item.completed","item":{"id":"i2","type":"agent_message","text":"Fixed the client."}}\n'`+"\n"+
			`printf '{"type":"turn.completed"}\n'`+"\n"+
			"exit 0")
	envVars := inEnvironment(append(setup.Env(), fixture.StubEnv(stubs, "codex")...))

	start := startJob(t, setup, envVars, "sweep", "--agent", "codex", "--", "fix the failing tests")
	if start.ExitCode != 0 {
		t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
	}
	await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "sweep", "--timeout", "30s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if await.ExitCode != 0 {
		t.Fatalf("await: exit %d: %s", await.ExitCode, await.Combined)
	}
	status := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "sweep"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "job/agent_progress_is_the_same_shape_for_every_tool", normalize.Apply(status.Combined))

	// The normalized fields are what an orchestrator branches on, and the
	// snapshot above renders them as one line; the record is the contract.
	assertCodexSweepProgressNormalized(t, setup)
}

func assertCodexSweepProgressNormalized(t *testing.T, setup env.Setup) {
	t.Helper()
	body, err := os.ReadFile(jobRecordPath(setup, "team", "dev", "sweep"))
	if err != nil {
		t.Fatalf("read job record: %v", err)
	}
	var record struct {
		Kind      string                 `json:"kind"`
		AgentTool string                 `json:"agentTool"`
		Progress  *normalizedJobProgress `json:"progress"`
	}
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatalf("parse job record: %v\n%s", err, body)
	}
	if record.Kind != "agent" || record.AgentTool != "codex" || record.Progress == nil {
		t.Fatalf("expected an agent record carrying progress, got:\n%s", body)
	}
	assertCodexFixProgress(t, *record.Progress)
}

// normalizedJobProgress is the tool-neutral progress an agent job record carries.
type normalizedJobProgress struct {
	Activity   string `json:"activity"`
	LastTool   string `json:"lastTool"`
	LastTarget string `json:"lastTarget"`
	ToolsRun   int    `json:"toolsRun"`
	Turns      int    `json:"turns"`
	Result     string `json:"result"`
}

func assertCodexFixProgress(t *testing.T, progress normalizedJobProgress) {
	t.Helper()
	if progress.LastTool != "Edit" || progress.LastTarget != "erun-common/mcp_client.go" {
		t.Errorf("codex file_change must normalize to the same tool/target a claude edit does, got %+v", progress)
	}
	if progress.ToolsRun != 1 || progress.Turns != 1 || progress.Result != "Fixed the client." {
		t.Errorf("unexpected normalized progress: %+v", progress)
	}
}

func testJobAgentProgressKeepsMovingAfterTheOutputCap(t *testing.T) {
	// The defect this closes: once outputTruncated flips, progress used to
	// freeze at whatever it last read, because it was derived from the same
	// capped log the raw output writer had stopped growing. The cap is set
	// small enough that the very first event already exceeds it, so every
	// later event proves progress is fed straight from the stream rather than
	// by re-reading the (now-frozen) log file.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	flushed := filepath.Join(setup.Cwd, "flushed")
	release := filepath.Join(setup.Cwd, "release")
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryWithScript(t, stubs, "claude",
		`printf '{"type":"assistant","message":{"id":"msg_1","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"erun-common/mcp_client.go"}}]}}\n'`+"\n"+
			"printf '"+jobStubSignal+"' > '"+flushed+"'\n"+
			"while [ ! -f '"+release+"' ]; do sleep 0.05; done\n"+
			`printf '{"type":"assistant","message":{"id":"msg_2","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go test ./..."}}]}}\n'`+"\n"+
			`printf '{"type":"result","subtype":"success","is_error":false,"num_turns":2,"result":"Fixed the client."}\n'`+"\n"+
			"exit 0")
	envVars := inEnvironment(append(setup.Env(), fixture.StubEnv(stubs, "claude")...))

	start := startJob(t, setup, envVars, "sweep", "--max-output-bytes", "8", "--agent", "claude", "--", "fix the failing tests")
	if start.ExitCode != 0 {
		t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
	}
	waitForFile(t, flushed, 30*time.Second)
	waitForJobActivity(t, setup, envVars, "sweep", 30*time.Second)

	assertSweepProgressPastTheCapMidRun(t, setup, envVars)

	if err := os.WriteFile(release, []byte("go\n"), 0o644); err != nil {
		t.Fatalf("release the stub: %v", err)
	}
	await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "sweep", "--timeout", "30s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if await.ExitCode != 0 {
		t.Fatalf("await: exit %d: %s", await.ExitCode, await.Combined)
	}
	finished := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "sweep", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	var finishedPayload struct {
		OutputTruncated bool `json:"outputTruncated"`
		Progress        struct {
			Turns  int    `json:"turns"`
			Result string `json:"result"`
		} `json:"progress"`
	}
	if err := json.Unmarshal([]byte(finished.Stdout), &finishedPayload); err != nil {
		t.Fatalf("parse finished job status JSON: %v\n%s", err, finished.Stdout)
	}
	if !finishedPayload.OutputTruncated {
		t.Fatalf("expected the job to still report outputTruncated at the end, got %s", finished.Stdout)
	}
	// This is the assertion the bug report's freeze would fail: the second
	// tool call and the final result both arrived well after the 8-byte cap,
	// so seeing them here proves progress kept moving past it.
	if finishedPayload.Progress.Turns != 2 {
		t.Fatalf("expected both turns folded despite the cap, got %+v", finishedPayload.Progress)
	}
	if finishedPayload.Progress.Result != "Fixed the client." {
		t.Fatalf("expected the closing result folded despite the cap, got %+v", finishedPayload.Progress)
	}
}

func assertSweepProgressPastTheCapMidRun(t *testing.T, setup env.Setup, envVars []string) {
	t.Helper()
	midRun := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "sweep", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	var midPayload struct {
		OutputTruncated bool `json:"outputTruncated"`
		Progress        struct {
			Activity string `json:"activity"`
		} `json:"progress"`
	}
	if err := json.Unmarshal([]byte(midRun.Stdout), &midPayload); err != nil {
		t.Fatalf("parse mid-run job status JSON: %v\n%s", err, midRun.Stdout)
	}
	if !midPayload.OutputTruncated {
		t.Fatalf("expected the 8-byte cap to already be hit by the first event, got %s", midRun.Stdout)
	}
	if !strings.Contains(midPayload.Progress.Activity, "erun-common/mcp_client.go") {
		t.Fatalf("expected progress to reflect the first tool call despite the cap, got %q", midPayload.Progress.Activity)
	}
}
