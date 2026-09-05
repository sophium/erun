package eruncommon

import "testing"

// TestRemoteAppSessionAttachLines pins the takeover contract a caller outside
// erun-cli/erun-common (the WSS session-attach gateway) reuses directly,
// independent of remoteShellLaunchLines's own byte-identical assertions.
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

// TestResolveAISessionAttachOutcome pins the mapping a WSS gateway uses to
// tell an evicted client it was taken over rather than merely disconnected —
// and, symmetrically, that a connection close with no exit status must read
// as unknown, never as a guessed Ended or TakenOver.
func TestResolveAISessionAttachOutcome(t *testing.T) {
	cases := []struct {
		name          string
		exitCode      int
		exitCodeKnown bool
		want          AISessionAttachOutcome
	}{
		{"taken over", remoteShellTakenOverExitCode, true, AISessionAttachOutcomeTakenOver},
		{"deploy reattach", remoteShellReattachDeployExitCode, true, AISessionAttachOutcomeDeployReattach},
		{"plain exit", 0, true, AISessionAttachOutcomeEnded},
		{"nonzero unrelated exit", 1, true, AISessionAttachOutcomeEnded},
		{"no exit status observed", 0, false, AISessionAttachOutcomeUnknown},
		{"no exit status observed, nonzero code ignored", remoteShellTakenOverExitCode, false, AISessionAttachOutcomeUnknown},
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
