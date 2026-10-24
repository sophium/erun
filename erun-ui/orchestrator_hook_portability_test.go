package main

import "testing"

func orchestratorAllHookCommands() []string {
	return []string{
		orchestratorSkillHookCommand("/tmp/orchestrators"),
		orchestratorActivityHookCommand(true),
		orchestratorActivityHookCommand(false),
		orchestratorShellActivityStartHookCommand(),
		orchestratorShellActivityClearHookCommand(),
		orchestratorShellActivityResetHookCommand(),
		orchestratorNoAskStopGuardCommand(),
	}
}

// Both host labels are tested so a POSIX-only branch cannot pass here and break Windows.
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
