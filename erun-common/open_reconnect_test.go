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
		if strings.Contains(script, "dtach") {
			t.Fatalf("bare `erun open` must not use dtach:\n%s", script)
		}
		if !strings.Contains(script, bashrc+" || shell_status=$?") {
			t.Fatalf("bare path lost its original bash invocation:\n%s", script)
		}
	})

	t.Run("ERun tab persists via dtach, plain shell", func(t *testing.T) {
		req := base
		req.AppSession = "open-0"
		script := preview(t, req)
		if !strings.Contains(script, `dtach -A "/tmp/erun-app/erun-local-open-0.dtach" -r winch /bin/bash`) {
			t.Fatalf("ERun tab missing dtach wrap:\n%s", script)
		}
		if strings.Contains(script, "claude") || strings.Contains(script, "ERUN_SKIP_LINT") {
			t.Fatalf("plain ERun shell must not launch claude or set contribute env:\n%s", script)
		}
		if !strings.Contains(script, "exec "+bashrc) {
			t.Fatalf("launcher must exec the interactive shell:\n%s", script)
		}
	})

	t.Run("AI tab launches claude once as the session program", func(t *testing.T) {
		req := base
		req.AppSession = "ai"
		req.AI = true
		script := preview(t, req)
		if !strings.Contains(script, `dtach -A "/tmp/erun-app/erun-local-ai.dtach"`) {
			t.Fatalf("AI tab missing dtach wrap:\n%s", script)
		}
		if !strings.Contains(script, "claude --continue --effort max") {
			t.Fatalf("AI tab must launch the claude guard at the env effort:\n%s", script)
		}
		if !strings.Contains(script, "exec "+bashrc) {
			t.Fatalf("AI launcher must drop to an interactive shell after claude:\n%s", script)
		}
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
		if !strings.Contains(script, "ERUN_SKIP_LINT") {
			t.Fatalf("contribute env missing:\n%s", script)
		}
	})
}
