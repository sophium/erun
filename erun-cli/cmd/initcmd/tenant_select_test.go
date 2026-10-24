package initcmd

import (
	"strings"
	"testing"

	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
)

// forceStdinSeams pins both halves of the "can this run ask?" question.
func forceStdinSeams(t *testing.T, terminal, hasAnswer bool) {
	t.Helper()
	prevTerminal, prevAnswer := shared.StdinIsTerminal, tenantPromptHasAnswer
	shared.StdinIsTerminal = func() bool { return terminal }
	tenantPromptHasAnswer = func() bool { return hasAnswer }
	t.Cleanup(func() {
		shared.StdinIsTerminal, tenantPromptHasAnswer = prevTerminal, prevAnswer
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

func TestSelectTenantPromptStillPromptsOnATerminal(t *testing.T) {
	forceStdinSeams(t, true, false)
	var shown promptui.Select
	called := false
	run := func(prompt promptui.Select) (int, string, error) {
		called = true
		shown = prompt
		return 1, "frs", nil
	}

	result, err := SelectTenantPrompt(run, []config.TenantConfig{{Name: "erun"}, {Name: "frs"}})
	if err != nil {
		t.Fatalf("selectTenantPrompt: %v", err)
	}
	if !called {
		t.Fatal("a real terminal must still prompt")
	}
	if result.Tenant != "frs" {
		t.Fatalf("tenant %q, want frs", result.Tenant)
	}
	items, ok := shown.Items.([]string)
	if !ok {
		t.Fatalf("prompt items %T, want []string", shown.Items)
	}
	want := []string{"erun", "frs", initializeCurrentProjectOption}
	if strings.Join(items, ",") != strings.Join(want, ",") {
		t.Fatalf("prompt items %v, want %v", items, want)
	}
}

func TestSelectTenantPromptSoleTenantStillPromptsOnATerminal(t *testing.T) {
	forceStdinSeams(t, true, false)
	called := false
	run := func(promptui.Select) (int, string, error) {
		called = true
		return 0, "erun", nil
	}

	if _, err := SelectTenantPrompt(run, []config.TenantConfig{{Name: "erun"}}); err != nil {
		t.Fatalf("selectTenantPrompt: %v", err)
	}
	if !called {
		t.Fatal("the interactive path must be unchanged, including for a sole tenant")
	}
}
