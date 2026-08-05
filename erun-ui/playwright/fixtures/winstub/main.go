// Command winstub is the Windows equivalent of the POSIX shell stubs in
// seedRoot.ts. Windows CreateProcess cannot exec a "#!/bin/sh" file or a
// .cmd/.bat batch file, so the backend (and every erun/shell child it spawns)
// needs a real PE executable on PATH. This single binary is copied to
// kubectl.exe / helm.exe / docker.exe / aws.exe / erun.exe in the isolated
// stub dir and dispatches on its own base name. Keep its behaviour in lockstep
// with writeStubBinary in fixtures/seedRoot.ts.
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
			fmt.Print("erun@playwright:~$ \n")
			_ = os.Stdout.Sync()
			blockForever() // stay alive like `exec sleep`; killed on env close via taskkill
		}
		os.Exit(0)
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
