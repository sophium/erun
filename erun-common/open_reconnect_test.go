package eruncommon

import (
	"strings"
	"testing"
)

// TestRemoteShellLaunchPersistentSession pins the reconnect contract: a desktop
// tab persists in a dtach session so another window can reattach, while the bare
// `erun open` CLI path stays unchanged.
func TestRemoteShellLaunchPersistentSession(t *testing.T) {
	base := ShellLaunchParams{
		Tenant:      "erun",
		Environment: "local",
		Title:       "erun/local",
		Namespace:   "erun-local",
		RemoteRepo:  true,
	}
	const bashrc = `/bin/bash --rcfile "$HOME/.erun/erun/local/bashrc" -i`

	preview := func(t *testing.T, req ShellLaunchParams) string {
		t.Helper()
		p, err := PreviewShellLaunch(req)
		if err != nil {
			t.Fatalf("PreviewShellLaunch: %v", err)
		}
		return p.Script
	}

	t.Run("bare CLI is unchanged (no dtach)", func(t *testing.T) {
		script := preview(t, base)
		assertScriptLacks(t, script, "dtach", "bare `erun open` must not use dtach")
		assertScriptHas(t, script, bashrc+" || shell_status=$?", "bare path lost its original bash invocation")
	})

	t.Run("ERun tab persists via dtach, plain shell", func(t *testing.T) {
		req := base
		req.AppSession = "open-0"
		script := preview(t, req)
		assertScriptHas(t, script, `dtach -A "/tmp/erun-sessions/erun-local-open-0.dtach" -r ctrl_l /bin/bash`, "ERun tab missing dtach wrap")
		if strings.Contains(script, "claude") {
			t.Fatalf("plain ERun shell must not launch claude:\n%s", script)
		}
		assertScriptHas(t, script, "exec "+bashrc, "launcher must exec the interactive shell")
	})

	t.Run("attach takes the session over from other windows", func(t *testing.T) {
		req := base
		req.AppSession = "open-0"
		script := preview(t, req)
		owner := `/tmp/erun-sessions/erun-local-open-0.owner`
		assertScriptHas(t, script, `printf '%s' "$attach_id" > "`+owner+`"`, "attach must claim the owner file")
		assertScriptHas(t, script, `if [ "$dtach_pid" != "$master_pid" ]`, "kick loop must spare the session master")
		assertScriptHas(t, script, `[ "$child_comm" != "dtach" ]`, "master detection must be the /proc child scan (runtime image has no ss)")
		assertScriptHas(t, script, `[ -S "/tmp/erun-sessions/erun-local-open-0.dtach" ] && [ -n "$master_pid" ]`, "kick must be skipped when the master cannot be identified")
		assertScriptHas(t, script, `!= "$attach_id" ]; then exit 76; fi`, "kicked wrapper must exit 76 on foreign owner")
	})

	t.Run("each terminal slot owns its own session", func(t *testing.T) {
		req := base
		req.AppSession = "open-1"
		script := preview(t, req)
		assertScriptHas(t, script, `dtach -A "/tmp/erun-sessions/erun-local-open-1.dtach" -r ctrl_l`, "slot 1 missing its own dtach socket")
		assertScriptLacks(t, script, "open-0", "slot 1 must not touch slot 0's session")
	})

	t.Run("AI tab launches claude once as the session program", func(t *testing.T) {
		req := base
		req.AppSession = "ai"
		req.AI = true
		script := preview(t, req)
		// AI tabs reattach with -r winch, not -r ctrl_l: Claude's main-screen TUI
		// ignores a bare ^L (and can mis-consume it as a keystroke), so only a
		// SIGWINCH redraws it.
		assertScriptHas(t, script, `dtach -A "/tmp/erun-sessions/erun-local-ai.dtach" -r winch`, "AI tab must reattach with -r winch so Claude repaints")
		assertScriptLacks(t, script, `erun-local-ai.dtach" -r ctrl_l`, "AI tab must not use -r ctrl_l (Claude ignores the bare ^L)")
		assertScriptHas(t, script, `claude --continue --settings '{"ultracode":true}'`, "AI tab must launch the claude guard at the default effort (ultracode)")
		// Claude's exit must surface to the user, not silently fall through to
		// the shell.
		if !strings.Contains(script, "fi || ai_status=$?") || !strings.Contains(script, "resume with: %s") {
			t.Fatalf("AI launcher missing the exit wrapper:\n%s", script)
		}
		assertScriptHas(t, script, "exec "+bashrc, "AI launcher must drop to an interactive shell after claude")
	})

	t.Run("contribute-AI tab cds to the clone, then claude", func(t *testing.T) {
		req := base
		req.AppSession = "contribute-ai"
		req.AI = true
		req.Contribute = true
		script := preview(t, req)
		clone := strings.Index(script, `cd "$HOME/git/erun"`)
		claude := strings.Index(script, "claude --continue")
		if clone < 0 || claude < 0 || clone > claude {
			t.Fatalf("contribute-AI must cd to the clone before launching claude:\n%s", script)
		}
		assertScriptHas(t, script, `export PATH="$HOME/.erun/contribute/bin:$PATH"`, "contribute env missing")
	})
}

// TestSessionSocketPathCannotCollideWithTheDesktopBinary pins the property, not
// the literal path: a session socket's name must share nothing with the desktop
// binary's process name. Freeing that binary for a rebuild invites
// `pkill -f erun-app`, and while the sockets lived under /tmp/erun-app that
// pattern matched every session's dtach command line and killed the operator's
// live terminals along with their agent sessions.
func TestSessionSocketPathCannotCollideWithTheDesktopBinary(t *testing.T) {
	paths := []string{
		RemoteAppSessionSocketDir,
		RemoteAppSessionSocketPath("erun", "local", "open-0"),
		RemoteAppSessionSocketPath("erun", "local", "ai"),
	}
	for _, path := range paths {
		if strings.Contains(path, DesktopAppName) {
			t.Fatalf("session path %q contains the desktop binary's process name %q; a pkill aimed at the binary would match a live session", path, DesktopAppName)
		}
	}
	// The dtach command line is what pkill actually matches, so the same
	// property is asserted on the rendered script rather than only on the path.
	script, err := PreviewShellLaunch(ShellLaunchParams{
		Tenant:      "erun",
		Environment: "local",
		Namespace:   "erun-local",
		RemoteRepo:  true,
		AppSession:  "open-0",
	})
	if err != nil {
		t.Fatalf("PreviewShellLaunch: %v", err)
	}
	if strings.Contains(script.Script, DesktopAppName) {
		t.Fatalf("the session launch script names the desktop binary %q:\n%s", DesktopAppName, script.Script)
	}
}

func assertScriptHas(t *testing.T, script, want, msg string) {
	t.Helper()
	if !strings.Contains(script, want) {
		t.Fatalf("%s:\n%s", msg, script)
	}
}

func assertScriptLacks(t *testing.T, script, unwanted, msg string) {
	t.Helper()
	if strings.Contains(script, unwanted) {
		t.Fatalf("%s:\n%s", msg, script)
	}
}

// TestParseRemoteAppSessionIDs pins the detection contract: parsing a pod's
// /tmp/erun-sessions listing yields only this env's dtach-socket session ids, so a
// fresh ERun window can rebuild tabs another window created.
func TestParseRemoteAppSessionIDs(t *testing.T) {
	lsOutput := strings.Join([]string{
		"erun-local-open-0.dtach",
		"erun-local-open-1.dtach",
		"erun-local-ai.dtach",
		"erun-local-ai.owner",
		"erun-local-contribute-ai.dtach",
		"other-env-open-2.dtach",
		"",
		"not-a-socket",
	}, "\n")
	got := ParseRemoteAppSessionIDs("erun", "local", lsOutput)
	want := []string{"open-0", "open-1", "ai", "contribute-ai"}
	if len(got) != len(want) {
		t.Fatalf("ParseRemoteAppSessionIDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ParseRemoteAppSessionIDs = %v, want %v", got, want)
		}
	}
}

// TestRemoteAppSessionEndScript pins the explicit-close contract: ending a
// custom terminal tears down its session so detection cannot rebuild the tab,
// and fails open — an unidentifiable master kills nothing but still drops the
// socket.
func TestRemoteAppSessionEndScript(t *testing.T) {
	script := RemoteAppSessionEndScript("erun", "local", "open-2")
	for _, want := range []string{
		`[ "$child_comm" != "dtach" ]`,
		`if [ -n "$master_pid" ]; then kill "$master_pid" 2>/dev/null || true; fi`,
		`rm -f "/tmp/erun-sessions/erun-local-open-2.dtach" "/tmp/erun-sessions/erun-local-open-2.owner"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("end script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "dtach -A") {
		t.Fatalf("end script must not attach:\n%s", script)
	}
}

// TestRemoteShellGitSeedKeepsPrivateKeyOffArgv pins the security contract: the
// private key never lands in a kubectl exec argv (visible via laptop `ps`, the
// pod's /proc/<pid>/cmdline, or exec audit logs) — it reaches the pod only on
// the separate seed exec's stdin.
func TestRemoteShellGitSeedKeepsPrivateKeyOffArgv(t *testing.T) {
	lines := remoteShellGitSeedScriptLines(
		`"$HOME/git/erun"`, "github.com", shellQuote("sophium"), shellQuote("erun"),
		"github.com ssh-ed25519 AAAAEXAMPLE",
	)
	script := strings.Join(lines, "\n")

	// The ssh config legitimately references the key via the tilde form
	// (`~/.ssh/keys`, a pointer, not key material), so the leak check must assert
	// on the $HOME form that the write/rm/chmod actually use.
	if strings.Contains(script, `"$HOME/.ssh/keys"`) {
		t.Fatalf("inline script must not write/rm/chmod the private key file:\n%s", script)
	}
	if strings.Contains(script, "PRIVATE KEY") {
		t.Fatalf("inline script must not contain private key material:\n%s", script)
	}
	if !strings.Contains(script, `cat > "$HOME/.ssh/known_hosts"`) ||
		!strings.Contains(script, `cat > "$HOME/.ssh/config"`) {
		t.Fatalf("inline script must still seed the public known_hosts + config:\n%s", script)
	}
	if !strings.Contains(script, "IdentityFile ~/.ssh/keys") {
		t.Fatalf("ssh config must still point at the seeded key file:\n%s", script)
	}
	if !strings.Contains(script, "git clone git@github.com:'sophium'/'erun'.git") {
		t.Fatalf("inline script must still clone the repo:\n%s", script)
	}
}

// TestRemoteSSHKeySeedArgsStreamOnStdin pins that the key-seed exec streams the
// key on stdin, not in the argv, so the key bytes never appear in a process
// listing.
func TestRemoteSSHKeySeedArgsStreamOnStdin(t *testing.T) {
	args := remoteSSHKeySeedArgs(ShellLaunchParams{Tenant: "erun", KubernetesContext: "ctx", Namespace: "erun-local"})

	sawI, sawIT := false, false
	for _, a := range args {
		if a == "-i" {
			sawI = true
		}
		if a == "-it" {
			sawIT = true
		}
	}
	if !sawI || sawIT {
		t.Fatalf("seed must use a non-interactive exec (-i, not -it): %v", args)
	}
	joined := strings.Join(args, "\x00")
	if !strings.Contains(joined, `cat > "$HOME/.ssh/keys"`) {
		t.Fatalf("seed program must write the key from stdin to ~/.ssh/keys: %v", args)
	}
	if !strings.Contains(joined, "umask 077") || !strings.Contains(joined, "chmod 600") {
		t.Fatalf("seed must create the key 0600 under a tight umask: %v", args)
	}
	if !strings.Contains(joined, "deployment/erun-devops") {
		t.Fatalf("seed must target the runtime deployment: %v", args)
	}
}
