package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/ai"
	"github.com/sophium/erun/erun-common/command/orchestrator"
	orchestratorsvc "github.com/sophium/erun/erun-ui/internal/orchestrator"
	"github.com/sophium/erun/erun-ui/internal/terminal"
)

const defaultOrchestratorFlags = ` --settings '{"ultracode":true}' --disallowedTools AskUserQuestion --model opus`

// defaultOrchestratorLaunch is what a transient session and an orchestrator with no settings launch on.
func defaultOrchestratorLaunch() orchestrator.OrchestratorLaunch {
	launch, _ := orchestrator.ResolveOrchestratorLaunch(config.OrchestratorConfig{}, nil)
	return launch
}

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
	if _, err := exec.LookPath(terminal.DefaultAITool); err != nil {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, terminal.DefaultAITool), []byte("#!/bin/sh\n"), 0o755); err != nil {
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
	stub := filepath.Join(dir, terminal.DefaultAITool)
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

func TestBuildOrchestratorLaunchInjectsMCPConfig(t *testing.T) {
	_, withMCP := buildOrchestratorLaunch("linux", defaultOrchestratorLaunch(), "", "/cfg/orchestrator-mcp-petios3.json")
	if joined := strings.Join(withMCP, " "); !strings.Contains(joined, `--mcp-config '/cfg/orchestrator-mcp-petios3.json'`) {
		t.Fatalf("expected --mcp-config in launch, got: %s", joined)
	}

	_, withoutMCP := buildOrchestratorLaunch("linux", defaultOrchestratorLaunch(), "", "")
	if joined := strings.Join(withoutMCP, " "); strings.Contains(joined, "--mcp-config") {
		t.Fatalf("expected no --mcp-config when path empty, got: %s", joined)
	}
}

func TestATransientSessionWithNoSettingsLaunchesExactlyAsBefore(t *testing.T) {
	launch := defaultOrchestratorLaunch()
	if !slices.Equal(launch.Env, []string{"CLAUDE_CODE_SUBAGENT_MODEL=opus"}) {
		t.Fatalf("expected the default env to be exactly the subagent model, got %q", launch.Env)
	}
	for _, goos := range []string{"linux", "windows"} {
		_, args := buildOrchestratorLaunch(goos, launch, "", "")
		if got := args[len(args)-1]; got != "claude"+defaultOrchestratorFlags {
			t.Fatalf("%s:\n got %q\nwant %q", goos, got, "claude"+defaultOrchestratorFlags)
		}
	}
}

func TestEveryEffortLevelReachesTheLaunchFlags(t *testing.T) {
	for _, level := range ai.ClaudeEffortLevels() {
		launch, err := orchestrator.ResolveOrchestratorLaunch(config.OrchestratorConfig{Model: "sonnet", Effort: level}, nil)
		if err != nil {
			t.Fatalf("effort %q: %v", level, err)
		}
		want := " --effort " + level + orchestratorsvc.NoAskFlag + " --model sonnet"
		if level == "ultracode" {
			want = ` --settings '{"ultracode":true}'` + orchestratorsvc.NoAskFlag + " --model sonnet"
		}
		for _, goos := range []string{"linux", "windows"} {
			_, args := buildOrchestratorLaunch(goos, launch, "", "")
			cmd := args[len(args)-1]
			if strings.Count(cmd, want) != 1 {
				t.Fatalf("%s effort %q: expected %q once, got %q", goos, level, want, cmd)
			}
			if level != "ultracode" && strings.Contains(cmd, "ultracode") {
				t.Fatalf("%s effort %q must not also launch ultracode: %q", goos, level, cmd)
			}
		}
	}
}
