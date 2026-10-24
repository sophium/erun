package open

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
)

// `pkill -f erun-app` must not match any session's dtach command line.
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

// An unidentifiable master kills nothing but still drops the socket.
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

func TestRemoteShellGitSeedKeepsPrivateKeyOffArgv(t *testing.T) {
	lines := RemoteShellGitSeedScriptLines(
		`"$HOME/git/erun"`, "github.com", core.ShellQuote("sophium"), core.ShellQuote("erun"),
		"github.com ssh-ed25519 AAAAEXAMPLE",
	)
	script := strings.Join(lines, "\n")

	// The ssh config's `~/.ssh/keys` is a pointer; writes use the $HOME form.
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
