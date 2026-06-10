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
		if !strings.Contains(script, `dtach -A "/tmp/erun-app/erun-local-open-0.dtach" -r ctrl_l /bin/bash`) {
			t.Fatalf("ERun tab missing dtach wrap:\n%s", script)
		}
		if strings.Contains(script, "claude") || strings.Contains(script, "ERUN_SKIP_LINT") {
			t.Fatalf("plain ERun shell must not launch claude or set contribute env:\n%s", script)
		}
		if !strings.Contains(script, "exec "+bashrc) {
			t.Fatalf("launcher must exec the interactive shell:\n%s", script)
		}
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
		if !strings.Contains(script, `printf '%s' "$attach_id" > "`+owner+`"`) {
			t.Fatalf("attach must claim the owner file:\n%s", script)
		}
		if !strings.Contains(script, `if [ "$dtach_pid" != "$master_pid" ]`) {
			t.Fatalf("kick loop must spare the session master:\n%s", script)
		}
		if !strings.Contains(script, `[ -S "/tmp/erun-app/erun-local-open-0.dtach" ] && [ -n "$master_pid" ]`) {
			t.Fatalf("kick must be skipped when the master cannot be identified:\n%s", script)
		}
		if !strings.Contains(script, `!= "$attach_id" ]; then exit 76; fi`) {
			t.Fatalf("kicked wrapper must exit 76 on foreign owner:\n%s", script)
		}
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
		if !strings.Contains(script, `dtach -A "/tmp/erun-app/erun-local-open-1.dtach" -r ctrl_l`) {
			t.Fatalf("slot 1 missing its own dtach socket:\n%s", script)
		}
		if strings.Contains(script, "open-0") {
			t.Fatalf("slot 1 must not touch slot 0's session:\n%s", script)
		}
	})

	t.Run("AI tab launches claude once as the session program", func(t *testing.T) {
		req := base
		req.AppSession = "ai"
		req.AI = true
		script := preview(t, req)
		if !strings.Contains(script, `dtach -A "/tmp/erun-app/erun-local-ai.dtach" -r ctrl_l`) {
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
