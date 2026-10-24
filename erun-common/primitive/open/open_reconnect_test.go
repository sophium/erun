package open

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
)

// TestRemoteShellLaunchPersistentSession pins the reconnect contract: a desktop
// tab persists in a dtach session so another window can reattach, while the bare
// `erun open` CLI path stays unchanged.
func TestRemoteShellLaunchPersistentSession(t *testing.T) {
	base := config.ShellLaunchParams{
		Tenant:      "erun",
		Environment: "local",
		Title:       "erun/local",
		Namespace:   "erun-local",
		RemoteRepo:  true,
	}
	const bashrc = `/bin/bash --rcfile "$HOME/.erun/erun/local/bashrc" -i`

	preview := func(t *testing.T, req config.ShellLaunchParams) string {
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

	t.Run("each terminal slot owns its own session", func(t *testing.T) {
		req := base
		req.AppSession = "open-1"
		script := preview(t, req)
		assertScriptHas(t, script, `dtach -A "/tmp/erun-sessions/erun-local-open-1.dtach" -r ctrl_l`, "slot 1 missing its own dtach socket")
		assertScriptLacks(t, script, "open-0", "slot 1 must not touch slot 0's session")
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
		activity.RemoteAppSessionSocketDir,
		RemoteAppSessionSocketPath("erun", "local", "open-0"),
		RemoteAppSessionSocketPath("erun", "local", "ai"),
	}
	for _, path := range paths {
		if strings.Contains(path, core.DesktopAppName) {
			t.Fatalf("session path %q contains the desktop binary's process name %q; a pkill aimed at the binary would match a live session", path, core.DesktopAppName)
		}
	}
	// The dtach command line is what pkill actually matches, so the same
	// property is asserted on the rendered script rather than only on the path.
	script, err := PreviewShellLaunch(config.ShellLaunchParams{
		Tenant:      "erun",
		Environment: "local",
		Namespace:   "erun-local",
		RemoteRepo:  true,
		AppSession:  "open-0",
	})
	if err != nil {
		t.Fatalf("PreviewShellLaunch: %v", err)
	}
	if strings.Contains(script.Script, core.DesktopAppName) {
		t.Fatalf("the session launch script names the desktop binary %q:\n%s", core.DesktopAppName, script.Script)
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
	lines := RemoteShellGitSeedScriptLines(
		`"$HOME/git/erun"`, "github.com", core.ShellQuote("sophium"), core.ShellQuote("erun"),
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
	args := RemoteSSHKeySeedArgs(config.ShellLaunchParams{Tenant: "erun", KubernetesContext: "ctx", Namespace: "erun-local"})

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
