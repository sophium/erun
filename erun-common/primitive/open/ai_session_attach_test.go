package open

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
)

// The WSS session-attach gateway reuses this takeover contract directly.
func TestRemoteAppSessionAttachLines(t *testing.T) {
	socket := "/tmp/erun-sessions/erun-local-ai.dtach"
	lines := RemoteAppSessionAttachLines(socket, "winch", `/bin/bash "$launch"`)
	script := ""
	for _, line := range lines {
		script += line + "\n"
	}

	assertScriptHas(t, script, `printf '%s' "$attach_id" > "/tmp/erun-sessions/erun-local-ai.owner"`, "attach must claim the owner file")
	assertScriptHas(t, script, `dtach -A "/tmp/erun-sessions/erun-local-ai.dtach" -r winch /bin/bash "$launch"`, "attach must reattach-or-create with the given redraw and launch command")
	assertScriptHas(t, script, `!= "$attach_id" ]; then exit 76; fi`, "a kicked attach must exit 76 on a foreign owner")
}

// A close with no exit status must read as unknown, never a guessed outcome.
func TestResolveAISessionAttachOutcome(t *testing.T) {
	cases := []struct {
		name          string
		exitCode      int
		exitCodeKnown bool
		want          model.AISessionAttachOutcome
	}{
		{"taken over", RemoteShellTakenOverExitCode, true, model.AISessionAttachOutcomeTakenOver},
		{"deploy reattach", RemoteShellReattachDeployExitCode, true, model.AISessionAttachOutcomeDeployReattach},
		{"plain exit", 0, true, model.AISessionAttachOutcomeEnded},
		{"nonzero unrelated exit", 1, true, model.AISessionAttachOutcomeEnded},
		{"no exit status observed", 0, false, model.AISessionAttachOutcomeUnknown},
		{"no exit status observed, nonzero code ignored", RemoteShellTakenOverExitCode, false, model.AISessionAttachOutcomeUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveAISessionAttachOutcome(tc.exitCode, tc.exitCodeKnown)
			if got != tc.want {
				t.Fatalf("ResolveAISessionAttachOutcome(%d, %v) = %q, want %q", tc.exitCode, tc.exitCodeKnown, got, tc.want)
			}
		})
	}
}
