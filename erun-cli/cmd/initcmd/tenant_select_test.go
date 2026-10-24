package initcmd

import (
	"testing"

	promptpkg "github.com/sophium/erun/cmd/shared/prompt"
)

// forceStdinSeams pins both halves of the "can this run ask?" question.
func forceStdinSeams(t *testing.T, terminal, hasAnswer bool) {
	t.Helper()
	prevTerminal, prevAnswer := promptpkg.StdinIsTerminal, tenantPromptHasAnswer
	promptpkg.StdinIsTerminal = func() bool { return terminal }
	tenantPromptHasAnswer = func() bool { return hasAnswer }
	t.Cleanup(func() {
		promptpkg.StdinIsTerminal, tenantPromptHasAnswer = prevTerminal, prevAnswer
	})
}

func TestTenantSelectionUnavailableFollowsStdin(t *testing.T) {
	cases := []struct {
		name      string
		terminal  bool
		hasAnswer bool
		want      bool
	}{
		{name: "terminal", terminal: true, hasAnswer: false, want: false},
		{name: "piped answer", terminal: false, hasAnswer: true, want: false},
		{name: "exhausted stdin", terminal: false, hasAnswer: false, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			forceStdinSeams(t, tc.terminal, tc.hasAnswer)
			if got := TenantSelectionUnavailable(); got != tc.want {
				t.Fatalf("tenantSelectionUnavailable() = %v, want %v", got, tc.want)
			}
		})
	}
}
