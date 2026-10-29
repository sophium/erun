package main

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/orchestrator"
	orchestratorsvc "github.com/sophium/erun/erun-ui/internal/orchestrator"
	"github.com/sophium/erun/erun-ui/internal/terminal"
)

// The orchestrator service's process defaults: the command an orchestrator PTY
// runs and the claude agent lifecycle commands. Tests replace both.

// orchestratorLaunchCommand runs through the host shell so npm .cmd/.ps1 shims
// resolve under ConPTY. A persisted orchestrator attaches to its background
// agent; a transient one runs a fresh session seeded with initialPrompt.
func orchestratorLaunchCommand(launch orchestrator.OrchestratorLaunch, agentID, initialPrompt, mcpConfigPath string) (string, []string, error) {
	if _, err := exec.LookPath(terminal.DefaultAITool); err != nil {
		return "", nil, fmt.Errorf("the %q CLI was not found on PATH; install it to run an orchestrator", terminal.DefaultAITool)
	}
	if strings.TrimSpace(agentID) != "" {
		if !claudeAgentIDPattern.MatchString(agentID) {
			return "", nil, fmt.Errorf("cannot attach to Claude background agent %q: not an agent id", agentID)
		}
		shell, args := orchestratorAttachCommand(runtime.GOOS, agentID)
		return shell, args, nil
	}
	shell, args := buildOrchestratorLaunch(runtime.GOOS, launch, initialPrompt, mcpConfigPath)
	return shell, args, nil
}

// claudeAgentIDPattern restricts the id because it is spliced into a shell line.
var claudeAgentIDPattern = regexp.MustCompile(`^[0-9a-f]{6,64}$`)

// buildOrchestratorLaunch splices model and effort unquoted, which is safe only
// because ResolveOrchestratorLaunch admits plain tokens.
func buildOrchestratorLaunch(goos string, launch orchestrator.OrchestratorLaunch, initialPrompt, mcpConfigPath string) (string, []string) {
	quote := terminal.ShellQuote
	if goos == "windows" {
		quote = terminal.PowerShellQuote
	}
	command := terminal.DefaultAITool + launch.EffortFlags + orchestratorsvc.NoAskFlag + " --model " + launch.Model
	if strings.TrimSpace(mcpConfigPath) != "" {
		command += " --mcp-config " + quote(mcpConfigPath)
	}
	if strings.TrimSpace(initialPrompt) != "" {
		command += " " + orchestratorPromptArg(goos, initialPrompt)
	}
	shell, shellArgs := terminal.LocalShellCommand(goos)
	flag := "-lc"
	if goos == "windows" {
		flag = "-Command"
	}
	return shell, append(shellArgs, flag, command)
}

// orchestratorPromptArg prefixes `--` so a multi-value flag cannot swallow the
// prompt, and shell-quotes it so `$`, backticks and backslashes survive verbatim.
func orchestratorPromptArg(goos, prompt string) string {
	prompt = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(prompt)
	if goos == "windows" {
		return "-- " + terminal.PowerShellQuote(prompt)
	}
	return "-- " + terminal.ShellQuote(prompt)
}

// Runs through the host shell: an npm claude.cmd shim only resolves there.
func orchestratorAttachCommand(goos, agentID string) (string, []string) {
	shell, shellArgs := terminal.LocalShellCommand(goos)
	flag := "-lc"
	if goos == "windows" {
		flag = "-Command"
	}
	return shell, append(shellArgs, flag, terminal.DefaultAITool+" attach "+agentID)
}

// Creating an agent may start Claude's background service first.
const claudeAgentCommandTimeout = 90 * time.Second

type execClaudeAgentCLI struct{}

// newDefaultClaudeAgents is a variable so the package's tests install their fake.
var newDefaultClaudeAgents = func() orchestratorsvc.AgentCLI { return execClaudeAgentCLI{} }

func (execClaudeAgentCLI) run(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), claudeAgentCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, terminal.DefaultAITool, args...)
	cmd.Dir = dir
	core.HideConsoleWindow(cmd)
	return cmd.CombinedOutput()
}

func (cli execClaudeAgentCLI) List() ([]orchestratorsvc.ClaudeAgent, error) {
	ctx, cancel := context.WithTimeout(context.Background(), claudeAgentCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, terminal.DefaultAITool, "agents", "--json", "--all")
	core.HideConsoleWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("cannot list Claude background agents: %w", err)
	}
	return orchestratorsvc.ParseClaudeAgents(out)
}

func (cli execClaudeAgentCLI) Create(dir string, args []string) (string, error) {
	out, err := cli.run(dir, args...)
	return orchestratorsvc.ParseClaudeAgentCreated(string(out), err, dir)
}

func (cli execClaudeAgentCLI) Stop(id string) error {
	if out, err := cli.run("", "stop", id); err != nil {
		return fmt.Errorf("claude stop %s: %w: %s", id, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (cli execClaudeAgentCLI) Remove(id string) error {
	if out, err := cli.run("", "rm", id); err != nil {
		return fmt.Errorf("claude rm %s: %w: %s", id, err, strings.TrimSpace(string(out)))
	}
	return nil
}
