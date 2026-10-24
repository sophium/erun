package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/command/orchestrator"
)

// A transient session is always fresh, never a resume.
func TestBuildOrchestratorLaunchStartsAFreshTransientSession(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		_, launch := buildOrchestratorLaunch(goos, defaultOrchestratorLaunch(), "", "")
		command := launch[len(launch)-1]
		for _, want := range []string{"ultracode", "--model opus"} {
			if !strings.Contains(command, want) {
				t.Fatalf("%s launch missing %q: %q", goos, want, command)
			}
		}
		for _, unwanted := range []string{"--continue", "--resume", "--session-id"} {
			if strings.Contains(command, unwanted) {
				t.Fatalf("%s transient launch must not resume (%s): %q", goos, unwanted, command)
			}
		}
	}
	_, seeded := buildOrchestratorLaunch("linux", defaultOrchestratorLaunch(), "fix the thing", "")
	if command := seeded[len(seeded)-1]; !strings.Contains(command, "-- 'fix the thing'") {
		t.Fatalf("seeded launch must hand over its prompt: %q", command)
	}
}

// The agent id is spliced into a shell line, so non-ids are refused.
func TestOrchestratorLaunchCommandAttachesOnlyToAnAgentID(t *testing.T) {
	if _, err := exec.LookPath(defaultAITool); err != nil {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, defaultAITool), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	_, args, err := orchestratorLaunchCommand(defaultOrchestratorLaunch(), "33cfe4e8", "", "/cfg/mcp.json")
	if err != nil {
		t.Fatalf("attach to a well-formed id: %v", err)
	}
	if command := args[len(args)-1]; command != "claude attach 33cfe4e8" {
		t.Fatalf("expected a bare attach by id, got %q", command)
	}
	for _, bad := range []string{"erun", "33cfe4e8; rm -rf ~", "33CFE4E8", "abc"} {
		if _, _, err := orchestratorLaunchCommand(defaultOrchestratorLaunch(), bad, "", ""); err == nil {
			t.Fatalf("expected %q to be refused as an agent id", bad)
		}
	}
}

// Orchestrators must not stop to ask; the harness denies the tool outright.
func TestBuildOrchestratorLaunchCannotStopToAsk(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		_, launch := buildOrchestratorLaunch(goos, defaultOrchestratorLaunch(), "", "")
		if command := launch[len(launch)-1]; !strings.Contains(command, "--disallowedTools AskUserQuestion") {
			t.Fatalf("%s launch must deny the ask tool: %q", goos, command)
		}
	}
	args := strings.Join(orchestratorAgentSpec{id: "erun", dir: "/o", settingsPath: "/s", mcpConfigPath: "/m"}.args(""), " ")
	if !strings.Contains(args, "--disallowedTools AskUserQuestion") {
		t.Fatalf("a background agent must deny the ask tool: %q", args)
	}
}

// The prompt must reach the harness as one unchanged argument: after `--` and shell-quoted.
func TestBuildOrchestratorLaunchHandsThePromptOverVerbatim(t *testing.T) {
	const prompt = "Run `erun-ui/build.sh`, read $HOME, keep C:\\tmp\\x and \"quotes\" and 'apostrophes'"

	_, launch := buildOrchestratorLaunch("linux", defaultOrchestratorLaunch(), prompt, "/cfg/orchestrator-mcp-erun.json")
	command := launch[len(launch)-1]
	if !strings.Contains(command, "--mcp-config '/cfg/orchestrator-mcp-erun.json' -- ") {
		t.Fatalf("the prompt must follow a -- that ends option parsing: %q", command)
	}
	if runtime.GOOS == "windows" {
		return
	}

	dir := t.TempDir()
	stub := filepath.Join(dir, defaultAITool)
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	shellCmd := exec.Command("/bin/sh", "-c", command)
	shellCmd.Env = []string{"PATH=" + dir, "HOME=/expanded-so-the-quoting-leaked"}
	out, err := shellCmd.Output()
	if err != nil {
		t.Fatalf("run the composed command: %v", err)
	}
	argv := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if got := argv[len(argv)-1]; got != prompt {
		t.Fatalf("the prompt did not survive the shell:\n got %q\nwant %q\nargv %v", got, prompt, argv)
	}
}

// PowerShell expands `$` and backticks inside double quotes, so values are single-quoted.
func TestBuildOrchestratorLaunchQuotesThePromptForPowerShell(t *testing.T) {
	_, launch := buildOrchestratorLaunch("windows", defaultOrchestratorLaunch(), "keep $HOME and `code` and it's fine", `C:\cfg\mcp.json`)
	command := launch[len(launch)-1]
	if !strings.Contains(command, "-- 'keep $HOME and `code` and it''s fine'") {
		t.Fatalf("windows prompt must be single-quoted with doubled apostrophes: %q", command)
	}
	if !strings.Contains(command, `--mcp-config 'C:\cfg\mcp.json'`) {
		t.Fatalf("windows mcp-config path must be single-quoted: %q", command)
	}
}

// A clean exit or a torn-down registration refuses respawn.
func TestOrchestratorReconnectRefusedGuards(t *testing.T) {
	app := NewApp(erunUIDeps{})
	id := "agent"
	key := orchestratorSessionKey(id)
	managed := &managedTerminal{key: key, kind: sessionKindOrchestrator}
	app.orchestrators[id] = &orchestratorSession{id: id}
	app.sessions[key] = managed

	if !app.orchestratorReconnectRefused(managed, "") {
		t.Fatal("a clean exit (empty reason) must refuse respawn")
	}
	if app.orchestratorReconnectRefused(managed, "exit status 1") {
		t.Fatal("a crash with an intact registration must not be refused")
	}

	delete(app.orchestrators, id)
	delete(app.sessions, key)
	if !app.orchestratorReconnectRefused(managed, "exit status 1") {
		t.Fatal("a torn-down registration must refuse respawn, so Stop cannot be undone")
	}
}

// A crash reattaches to the same agent without a prompt.
func TestOrchestratorRespawnsAfterCrashOntoTheSameAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	var mu sync.Mutex
	var sessions []*stubTerminalSession
	var attached, prompts []string
	app := NewApp(erunUIDeps{
		store: newOrchestratorStubStore(t.TempDir()),
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			session := newStubTerminalSession()
			mu.Lock()
			sessions = append(sessions, session)
			mu.Unlock()
			return session, nil
		},
		resolveOrchestratorLaunch: func(_ orchestrator.OrchestratorLaunch, agentID, initialPrompt, _ string) (string, []string, error) {
			mu.Lock()
			attached = append(attached, agentID)
			prompts = append(prompts, initialPrompt)
			mu.Unlock()
			return "claude-stub", nil, nil
		},
		runOrchestratorLabelCommand: stubOrchestratorClaimLabel,
	})
	defer app.shutdown(context.Background())
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorLaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}

	mu.Lock()
	first := sessions[0]
	first.waitErr = errors.New("exit status 1")
	mu.Unlock()
	_ = first.Close()

	waitForSessionCount(t, &mu, &sessions, 2, 2*time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(attached) != 2 || attached[0] == "" || attached[0] != attached[1] {
		t.Fatalf("respawn must reattach to the SAME agent, got %v", attached)
	}
	if prompts[1] != "" {
		t.Fatalf("a reattach must not send the agent a prompt, got %q", prompts[1])
	}
	if creates := len(fakeAgentsOf(t, app).creates); creates != 1 {
		t.Fatalf("expected the agent created once and reattached, got %d creates", creates)
	}
}

func TestOrchestratorCleanExitDoesNotRespawn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	var mu sync.Mutex
	var sessions []*stubTerminalSession
	app := NewApp(erunUIDeps{
		store: newOrchestratorStubStore(t.TempDir()),
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			session := newStubTerminalSession()
			mu.Lock()
			sessions = append(sessions, session)
			mu.Unlock()
			return session, nil
		},
		resolveOrchestratorLaunch: func(orchestrator.OrchestratorLaunch, string, string, string) (string, []string, error) {
			return "claude-stub", nil, nil
		},
		runOrchestratorLabelCommand: stubOrchestratorClaimLabel,
	})
	defer app.shutdown(context.Background())
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorLaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}

	mu.Lock()
	first := sessions[0]
	mu.Unlock()
	_ = first.Close() // waitErr is nil: a clean exit

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(emits.events(terminalExitEvent)) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(emits.events(terminalExitEvent)) == 0 {
		t.Fatal("expected the clean exit to finalize the session")
	}
	mu.Lock()
	got := len(sessions)
	mu.Unlock()
	if got != 1 {
		t.Fatalf("a clean exit must not respawn, got %d sessions", got)
	}
}

func TestStopOrchestratorRefusesItsOwnRespawn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	var mu sync.Mutex
	var sessions []*stubTerminalSession
	app := NewApp(erunUIDeps{
		store: newOrchestratorStubStore(t.TempDir()),
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			session := newStubTerminalSession()
			mu.Lock()
			sessions = append(sessions, session)
			mu.Unlock()
			return session, nil
		},
		resolveOrchestratorLaunch: func(orchestrator.OrchestratorLaunch, string, string, string) (string, []string, error) {
			return "claude-stub", nil, nil
		},
		runOrchestratorLabelCommand: stubOrchestratorClaimLabel,
	})
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorLaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}

	if err := app.StopOrchestrator(created.ID); err != nil {
		t.Fatalf("StopOrchestrator failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	got := len(sessions)
	mu.Unlock()
	if got != 1 {
		t.Fatalf("Stop must not be undone by a respawn, got %d sessions", got)
	}
	if info, ok := app.runningOrchestratorInfo(created.ID); ok {
		t.Fatalf("expected no running session after Stop, got %+v", info)
	}
}
