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

func main() {
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe")
	args := os.Args[1:]

	switch name {
	case "erun":
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
			fmt.Print("erun@playwright:~$ \n")
			_ = os.Stdout.Sync()
			blockForever() // stay alive like `exec sleep`; ended by the harness reap or env close
		}
		os.Exit(0)
	case "claude":
		// An orchestrator row reads "running" only while the session the desktop
		// spawned is live, and the real binary cannot stay up on a harness host
		// (no TTY, no credentials) — it exits at once and the spec that opened
		// the orchestrator times out waiting for the running dot. Block instead,
		// printing the same setup-complete marker the POSIX stub does.
		registerStub(name)
		fmt.Print("claude@playwright:~$ \n")
		_ = os.Stdout.Sync()
		blockForever() // stay alive like `exec sleep`; ended by the harness reap or session close
	case "kubectl":
		// Answer the context listing with an empty set (the dialog's
		// deterministic empty state); report everything else as unreachable.
		if strings.Contains(strings.Join(args, " "), "config get-contexts") {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "kubectl stub: no cluster in the Playwright harness")
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "%s stub: disabled in the Playwright harness\n", name)
		os.Exit(1)
	}
}
