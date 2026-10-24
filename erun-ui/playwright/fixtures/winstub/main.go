// Command winstub is a PE stand-in for seedRoot.ts's POSIX stubs, dispatching on its base name.
// Keep in lockstep with writeStubBinary in fixtures/seedRoot.ts.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// blockForever must not use select{}: the runtime would flag a deadlock and crash.
func blockForever() {
	for {
		time.Sleep(time.Hour)
	}
}

// registerStub records the pid so the harness can reap leaked stubs; best-effort.
// Keep in lockstep with the POSIX register_stub preamble in seedRoot.ts.
func registerStub(name string) {
	registry := os.Getenv("ERUN_PLAYWRIGHT_STUB_REGISTRY")
	if registry == "" {
		return
	}
	f, err := os.OpenFile(registry, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "%d %s\n", os.Getpid(), name)
}

// waitForTabSetupRelease blocks while the spec holds `<tenant>.<environment>`.
func waitForTabSetupRelease(args []string) {
	dir := os.Getenv("ERUN_PLAYWRIGHT_TAB_SETUP_HOLDS")
	if dir == "" || len(args) < 3 {
		return
	}
	hold := filepath.Join(dir, args[1]+"."+args[2])
	for {
		if _, err := os.Stat(hold); err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func main() {
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe")
	args := os.Args[1:]

	switch name {
	case "erun":
		runErunStub(name, args)
	case "claude":
		runClaudeStub(name, args)
	case "kubectl":
		if strings.Contains(strings.Join(args, " "), "config get-contexts") {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "kubectl stub: no cluster in the Playwright harness")
		os.Exit(1)
	case "explorer":
		recordOpenerRequest(args)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "%s stub: disabled in the Playwright harness\n", name)
		os.Exit(1)
	}
}

func runErunStub(name string, args []string) {
	if len(args) >= 1 && args[0] == "open" {
		joined := strings.Join(args, " ")
		// `--no-shell` is a probe the caller waits on; blocking it would hang env-runtime ensure.
		if strings.Contains(joined, "--no-shell") {
			os.Exit(0)
		}
		// Print the setup-complete marker, then block like a live shell.
		registerStub(name)
		waitForTabSetupRelease(args)
		fmt.Print("erun@playwright:~$ \n")
		_ = os.Stdout.Sync()
		blockForever() // stay alive like `exec sleep`; ended by the harness reap or env close
	}
	os.Exit(0)
}

func runClaudeStub(name string, args []string) {
	// Background-agent commands answer from the agent file; attach and transient sessions
	// block, since a row reads "running" only while its session is live.
	if claudeAgentCommand(args) {
		os.Exit(0)
	}
	registerStub(name)
	fmt.Print("claude@playwright:~$ \n")
	_ = os.Stdout.Sync()
	blockForever() // stay alive like `exec sleep`; ended by the harness reap or session close
}

func recordOpenerRequest(args []string) {
	log := os.Getenv("ERUN_PLAYWRIGHT_OPENER_LOG")
	if log == "" {
		return
	}
	f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(f, strings.Join(args, " "))
	_ = f.Close()
}

// claudeAgentsFile stands in for Claude Code's agent service: `<id> <run|stopped> <name>` per line.
// Keep in lockstep with claudeAgentsPath in seedRoot.ts.
func claudeAgentsFile() string {
	if path := os.Getenv("ERUN_PLAYWRIGHT_CLAUDE_AGENTS"); path != "" {
		return path
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude-playwright-agents")
}

type claudeStubAgent struct{ id, state, name string }

func readClaudeStubAgents() []claudeStubAgent {
	data, err := os.ReadFile(claudeAgentsFile())
	if err != nil {
		return nil
	}
	var out []claudeStubAgent
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 {
			out = append(out, claudeStubAgent{fields[0], fields[1], fields[2]})
		}
	}
	return out
}

func writeClaudeStubAgents(agents []claudeStubAgent) {
	var b strings.Builder
	for _, agent := range agents {
		fmt.Fprintf(&b, "%s %s %s\n", agent.id, agent.state, agent.name)
	}
	_ = os.WriteFile(claudeAgentsFile(), []byte(b.String()), 0o644)
}

func setClaudeStubAgentState(id, state string) {
	agents := readClaudeStubAgents()
	kept := agents[:0]
	for _, agent := range agents {
		if agent.id == id {
			if state == "gone" {
				continue
			}
			agent.state = state
		}
		kept = append(kept, agent)
	}
	writeClaudeStubAgents(kept)
}

// claudeAgentCommand reports whether args were a lifecycle command; `attach` falls through to block.
func claudeAgentCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "agents":
		printClaudeStubAgents()
		return true
	case "--bg":
		startClaudeStubAgent(args)
		return true
	case "stop", "rm":
		if len(args) > 1 {
			state, verb := "stopped", "stopped"
			if args[0] == "rm" {
				state, verb = "gone", "removed"
			}
			setClaudeStubAgentState(args[1], state)
			fmt.Printf("%s %s\n", verb, args[1])
		}
		return true
	case "attach":
		if len(args) > 1 {
			setClaudeStubAgentState(args[1], "run")
		}
		return false
	}
	return false
}

func printClaudeStubAgents() {
	parts := make([]string, 0)
	for _, agent := range readClaudeStubAgents() {
		pid := ""
		if agent.state == "run" {
			pid = `"pid":1,`
		}
		parts = append(parts, fmt.Sprintf(`{%s"id":"%s","kind":"background","name":"%s","sessionId":"%s-0000-4000-8000-000000000000"}`, pid, agent.id, agent.name, agent.id))
	}
	fmt.Println("[" + strings.Join(parts, ",") + "]")
}

func startClaudeStubAgent(args []string) {
	name := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-n" {
			name = args[i+1]
		}
	}
	id := fmt.Sprintf("%08x", os.Getpid())
	writeClaudeStubAgents(append(readClaudeStubAgents(), claudeStubAgent{id, "run", name}))
	fmt.Printf("backgrounded · %s · %s\n", id, name)
}
