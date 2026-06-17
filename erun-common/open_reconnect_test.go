package eruncommon

import (
	"strings"
	"testing"
)

// TestRemoteShellLaunchPersistentSession pins the #478 reconnect mechanism: a
// desktop tab (AppSession set) runs the remote shell inside a persistent dtach
// session keyed by the id, with the per-tab prelude (contribute cd, AI launch)
// as the session's create-time program; the bare `erun open` CLI path is left
// byte-for-byte unchanged.
func TestRemoteShellLaunchPersistentSession(t *testing.T) {
	base := ShellLaunchParams{
		Tenant:      "erun",
		Environment: "local",
		Title:       "erun/local",
		Namespace:   "erun-local",
		RemoteRepo:  true, // skip the git-seed lines; focus on the launch path
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
		assertScriptHas(t, script, `dtach -A "/tmp/erun-app/erun-local-open-0.dtach" -r ctrl_l /bin/bash`, "ERun tab missing dtach wrap")
		if strings.Contains(script, "claude") || strings.Contains(script, "ERUN_SKIP_LINT") {
			t.Fatalf("plain ERun shell must not launch claude or set contribute env:\n%s", script)
		}
		assertScriptHas(t, script, "exec "+bashrc, "launcher must exec the interactive shell")
	})

	t.Run("attach takes the session over from other windows", func(t *testing.T) {
		// screen-style detach-elsewhere-and-reattach-here: the attach claims
		// an owner id, detaches other viewers by killing their dtach clients
		// (never the master, which owns the running shell/claude — and no one
		// when the master cannot be identified), and a kicked wrapper sees the
		// foreign owner id and exits 76 so its window reports the handover.
		req := base
		req.AppSession = "open-0"
		script := preview(t, req)
		owner := `/tmp/erun-app/erun-local-open-0.owner`
		assertScriptHas(t, script, `printf '%s' "$attach_id" > "`+owner+`"`, "attach must claim the owner file")
		assertScriptHas(t, script, `if [ "$dtach_pid" != "$master_pid" ]`, "kick loop must spare the session master")
		assertScriptHas(t, script, `[ "$child_comm" != "dtach" ]`, "master detection must be the /proc child scan (runtime image has no ss)")
		assertScriptHas(t, script, `[ -S "/tmp/erun-app/erun-local-open-0.dtach" ] && [ -n "$master_pid" ]`, "kick must be skipped when the master cannot be identified")
		assertScriptHas(t, script, `!= "$attach_id" ]; then exit 76; fi`, "kicked wrapper must exit 76 on foreign owner")
	})

	t.Run("each terminal slot owns its own session", func(t *testing.T) {
		// One env carries exactly one session per id: custom "Terminal N" tabs
		// get distinct slot ids, and a second ERun window passing the same id
		// attaches to the same socket — taking the session over rather than
		// spawning a parallel one. The socket key is tenant+env+id only, never
		// anything app-instance-specific, and a takeover for one id must not
		// touch any other slot's session.
		req := base
		req.AppSession = "open-1"
		script := preview(t, req)
		assertScriptHas(t, script, `dtach -A "/tmp/erun-app/erun-local-open-1.dtach" -r ctrl_l`, "slot 1 missing its own dtach socket")
		assertScriptLacks(t, script, "open-0", "slot 1 must not touch slot 0's session")
	})

	t.Run("AI tab launches claude once as the session program", func(t *testing.T) {
		req := base
		req.AppSession = "ai"
		req.AI = true
		script := preview(t, req)
		assertScriptHas(t, script, `dtach -A "/tmp/erun-app/erun-local-ai.dtach" -r ctrl_l`, "AI tab missing dtach wrap")
		assertScriptHas(t, script, `claude --continue --settings '{"ultracode":true}'`, "AI tab must launch the claude guard at the default effort (ultracode)")
		// Claude's exit must not silently fall through to the shell: the
		// wrapper names the exit and the resume command first (issue #464).
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
		assertScriptHas(t, script, "ERUN_SKIP_LINT", "contribute env missing")
	})
}

// assertScriptHas fails the test when script does not contain want, reporting
// msg followed by the full script.
func assertScriptHas(t *testing.T, script, want, msg string) {
	t.Helper()
	if !strings.Contains(script, want) {
		t.Fatalf("%s:\n%s", msg, script)
	}
}

// assertScriptLacks fails the test when script contains unwanted, reporting
// msg followed by the full script.
func assertScriptLacks(t *testing.T, script, unwanted, msg string) {
	t.Helper()
	if strings.Contains(script, unwanted) {
		t.Fatalf("%s:\n%s", msg, script)
	}
}

// TestParseRemoteAppSessionIDs pins the detection contract: a pod's
// /tmp/erun-app listing yields exactly this env's session ids — dtach sockets
// only, owner files and other envs' sockets ignored — so a fresh ERun window
// can rebuild tabs for sessions another window created.
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
// custom terminal kills the session master (the program follows via SIGHUP)
// and removes the socket and owner file, so detection cannot rebuild the tab.
// The master scan is the same /proc child scan the attach uses; an
// unidentifiable master means nothing is killed (fail open) but the socket
// still goes away.
func TestRemoteAppSessionEndScript(t *testing.T) {
	script := RemoteAppSessionEndScript("erun", "local", "open-2")
	for _, want := range []string{
		`[ "$child_comm" != "dtach" ]`,
		`if [ -n "$master_pid" ]; then kill "$master_pid" 2>/dev/null || true; fi`,
		`rm -f "/tmp/erun-app/erun-local-open-2.dtach" "/tmp/erun-app/erun-local-open-2.owner"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("end script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "dtach -A") {
		t.Fatalf("end script must not attach:\n%s", script)
	}
}

// TestRemoteShellGitSeedKeepsPrivateKeyOffArgv pins the security fix: the
// inline bootstrap script seeds only the public known_hosts + ssh config and
// clones the repo; it never writes the private key. The key reaches the pod
// only via the separate seed exec's stdin, so it can never land in a kubectl
// exec argv (laptop `ps`, the pod's /proc/<pid>/cmdline, or exec audit logs).
func TestRemoteShellGitSeedKeepsPrivateKeyOffArgv(t *testing.T) {
	lines := remoteShellGitSeedScriptLines(
		`"$HOME/git/erun"`, "github.com", shellQuote("sophium"), shellQuote("erun"),
		"github.com ssh-ed25519 AAAAEXAMPLE",
	)
	script := strings.Join(lines, "\n")

	// The script must not WRITE / rm / chmod ~/.ssh/keys ($HOME form). The ssh
	// config still *references* the key file via `~/.ssh/keys` (tilde) — that is
	// a pointer, not the key material — so assert on the $HOME form the
	// write/rm/chmod used.
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

// TestRemoteSSHKeySeedArgsStreamOnStdin pins that the key-seed exec is a
// non-interactive `kubectl exec -i` whose program reads the key from stdin
// (`cat > ~/.ssh/keys`) — so the key bytes never appear in the argv.
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
