// Command winstub is the Windows equivalent of the POSIX shell stubs in
// seedRoot.ts. Windows CreateProcess cannot exec a "#!/bin/sh" file or a
// .cmd/.bat batch file, so the backend (and every erun/shell child it spawns)
// needs a real PE executable on PATH. This single binary is copied to
// kubectl.exe / helm.exe / docker.exe / aws.exe / erun.exe / claude.exe in the
// isolated stub dir and dispatches on its own base name. Keep its behaviour in
// lockstep with writeStubBinary in fixtures/seedRoot.ts.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// blockForever keeps the stub process alive like the POSIX `exec sleep` does.
// It must NOT use `select{}` / `<-make(chan)`: with no other goroutines the Go
// runtime's deadlock detector treats those as a fatal deadlock and crashes the
// process the instant the prompt is printed, so the desktop sees the session
// exit and spins into reconnect/respawn churn. An infinite timer sleep parks a
// live process the runtime never flags.
func blockForever() {
	for {
		time.Sleep(time.Hour)
	}
}

// registerStub records this process's pid, and which stub it is, in the
// harness's stub registry, so a session the suite opened and never closed can be
// reaped (fixtures/stubProcesses.ts). Best-effort: outside the harness there is
// no registry in the environment and the stub must still run. Keep in lockstep
// with the POSIX register_stub preamble in fixtures/seedRoot.ts.
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

// waitForTabSetupRelease holds an interactive tab short of its setup-complete
// marker while a spec holds `<tenant>.<environment>` in the hold directory, the
// same as the POSIX stub (holdTabSetup in fixtures/seedRoot.ts).
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
		// Answer the context listing with an empty set (the dialog's
		// deterministic empty state); report everything else as unreachable.
		if strings.Contains(strings.Join(args, " "), "config get-contexts") {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "kubectl stub: no cluster in the Playwright harness")
		os.Exit(1)
	case "explorer":
		// The desktop's host opener: record the request and open nothing.
		recordOpenerRequest(args)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "%s stub: disabled in the Playwright harness\n", name)
		os.Exit(1)
	}
}

// runErunStub answers the desktop's erun invocations: a probe exits, and an
// interactive tab session blocks like a live shell.
func runErunStub(name string, args []string) {
	if len(args) >= 1 && args[0] == "open" {
		joined := strings.Join(args, " ")
		// `erun open --no-shell` is a non-interactive probe the desktop runs to
		// (re)establish the env's MCP/port-forwards or sshd — reconnectMCP /
		// ensureEnvRuntime / the idle probe (see runOpenForReconnect,
		// ensureSSHDViaOpenCommand). The real CLI sets things up and EXITS; the
		// caller waits on that exit. Blocking it here would hang env-runtime
		// ensure and spin the sidebar into perpetual "reconnecting" busy. So the
		// probe form completes successfully and exits.
		if strings.Contains(joined, "--no-shell") {
			os.Exit(0)
		}
		// An interactive tab session (ERun/AI, `--app-session`) is the desktop's
		// long-lived shell. Print the shell-prompt line the action runner treats
		// as the setup-complete marker (see signalSessionReadyOnLine in
		// activity_queue_app.go), then block so the tab behaves like a healthy,
		// quiet, killable session.
		registerStub(name)
		waitForTabSetupRelease(args)
		fmt.Print("erun@playwright:~$ \n")
		_ = os.Stdout.Sync()
		blockForever() // stay alive like `exec sleep`; ended by the harness reap or env close
	}
	os.Exit(0)
}

// runClaudeStub answers the desktop's claude invocations.
func runClaudeStub(name string, args []string) {
	// A persisted orchestrator runs as a background agent the desktop
	// lists, creates, stops, removes and attaches to through this binary,
	// so those commands are answered from the stub's agent file, the same
	// as the POSIX stub. An orchestrator row reads "running" only while
	// the session the desktop spawned is live, and the real binary cannot
	// stay up on a harness host (no TTY, no credentials) — so `attach`,
	// and a transient session, block instead, printing the same
	// setup-complete marker the POSIX stub does.
	if claudeAgentCommand(args) {
		os.Exit(0)
	}
	registerStub(name)
	fmt.Print("claude@playwright:~$ \n")
	_ = os.Stdout.Sync()
	blockForever() // stay alive like `exec sleep`; ended by the harness reap or session close
}

// recordOpenerRequest appends the opener's arguments to the log the harness
// reads back, mirroring the POSIX open/xdg-open stubs.
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

// claudeAgentsFile is the stub's stand-in for Claude Code's background service:
// one line per agent, `<id> <run|stopped> <name>`. Keep in lockstep with the
// POSIX claude stub and claudeAgentsPath in fixtures/seedRoot.ts.
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

// setClaudeStubAgentState moves one agent to state, or drops it for "gone".
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

// claudeAgentCommand answers a background-agent lifecycle command and reports
// whether it was one; `attach` marks the agent running and then falls through
// to the blocking session.
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

// printClaudeStubAgents answers `claude agents` from the stub's agent file.
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

// startClaudeStubAgent answers `claude --bg -n <name>` by recording a running agent.
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
