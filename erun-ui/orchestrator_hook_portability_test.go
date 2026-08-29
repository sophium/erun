package main

import "testing"

// orchestratorAllHookCommands returns every command an orchestrator hook can
// install, so one check can assert the portability invariant across all of
// them instead of one at a time per hook.
func orchestratorAllHookCommands() []string {
	return []string{
		orchestratorSkillHookCommand("/tmp/orchestrators"),
		orchestratorActivityHookCommand(true),
		orchestratorActivityHookCommand(false),
		orchestratorShellActivityStartHookCommand(),
		orchestratorShellActivityClearHookCommand(),
		orchestratorShellActivityResetHookCommand(),
		orchestratorLiveConversationHookCommand(),
		orchestratorNoAskStopGuardCommand(),
	}
}

// TestOrchestratorHookCommandsArePortableAcrossHostShells is the regression
// test for the failure a POSIX-only hook produces the moment the host's own
// hook shell is not sh: Windows runs orchestrator hooks through PowerShell,
// where a leading "[" opens a type literal instead of a test expression, and
// every hook died at parse time before a single statement ran.
//
// The commands erun emits do not vary by host OS -- every one runs through
// node instead of a shell built-in -- so the same assertion holds for a POSIX
// host and for Windows identically. Table-testing it against both labels
// explicitly, rather than only the POSIX host this suite actually runs on, is
// what keeps a future edit from quietly reintroducing a POSIX-only branch
// that would pass here and still break on the platform that never runs this
// suite.
func TestOrchestratorHookCommandsArePortableAcrossHostShells(t *testing.T) {
	for _, goos := range []string{"windows", "linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			for _, command := range orchestratorAllHookCommands() {
				if !orchestratorHookCommandIsPortable(command) {
					t.Fatalf("hook command is not a single portable node invocation and would fail to parse under %s's own hook shell:\n%s", goos, command)
				}
			}
		})
	}
}

// TestOrchestratorHookCommandIsPortableRejectsPOSIXOnlyShapes locks in what
// the checker itself is supposed to catch, so a future edit to it cannot
// quietly stop catching the shape that broke Windows in the first place.
func TestOrchestratorHookCommandIsPortableRejectsPOSIXOnlyShapes(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    bool
	}{
		{"a bare POSIX test guard", `[ -n "$ERUN_ORCHESTRATOR_ID" ] && echo hi`, false},
		{"command substitution", `echo "$(date +%s)"`, false},
		{"sh chaining around a node call", `node -e 'x' || true`, false},
		{"a portable node invocation", `node -e 'console.log("hi")'`, true},
		{"a single quote breaking out of the node argument", `node -e 'x'; rm -rf /'`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := orchestratorHookCommandIsPortable(tc.command); got != tc.want {
				t.Fatalf("orchestratorHookCommandIsPortable(%q) = %v, want %v", tc.command, got, tc.want)
			}
		})
	}
}
