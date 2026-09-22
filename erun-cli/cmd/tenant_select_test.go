package cmd

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/internal"
)

// forceStdinSeams pins both halves of the "can this run ask?" question, the way
// ERUN_FORCE_TTY pins writerIsTerminal for the open guard.
func forceStdinSeams(t *testing.T, terminal, hasAnswer bool) {
	t.Helper()
	prevTerminal, prevAnswer := stdinIsTerminal, tenantPromptHasAnswer
	stdinIsTerminal = func() bool { return terminal }
	tenantPromptHasAnswer = func() bool { return hasAnswer }
	t.Cleanup(func() {
		stdinIsTerminal, tenantPromptHasAnswer = prevTerminal, prevAnswer
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
			if got := tenantSelectionUnavailable(); got != tc.want {
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

	result, err := selectTenantPrompt(run, []common.TenantConfig{{Name: "erun"}, {Name: "frs"}})
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

	if _, err := selectTenantPrompt(run, []common.TenantConfig{{Name: "erun"}}); err != nil {
		t.Fatalf("selectTenantPrompt: %v", err)
	}
	if !called {
		t.Fatal("the interactive path must be unchanged, including for a sole tenant")
	}
}

func TestBootstrapInitExitErrorKeepsTheOutcomesDistinct(t *testing.T) {
	unavailable := common.TenantSelectionUnavailableError{Tenants: []string{"erun", "frs"}}
	tagged := bootstrapInitExitError(unavailable)
	if got := internal.ExitCodeFor(tagged); got != tenantSelectionUnavailableExitCode {
		t.Fatalf("exit code %d, want %d", got, tenantSelectionUnavailableExitCode)
	}
	if errors.Is(tagged, common.ErrPlatformAliasUnusable) {
		t.Fatal("a tenant refusal must not report the platform-alias condition")
	}

	aliasErr := fmt.Errorf("multiple erun platform cloud provider aliases are configured; pass --erun-alias to choose one: %w", common.ErrPlatformAliasUnusable)
	passed := bootstrapInitExitError(aliasErr)
	if !errors.Is(passed, common.ErrPlatformAliasUnusable) {
		t.Fatal("an unresolvable platform alias must keep its own sentinel to report 127")
	}
	if errors.Is(passed, common.ErrNotInGitRepository) {
		t.Fatal("alias failure was reclassified")
	}
}

func TestTenantSelectionUnavailableExitCodeIsItsOwnContract(t *testing.T) {
	// A script that reads only the exit code branches on this number, so it is
	// pinned here: not an ordinary failure (1), not the platform-alias
	// condition main.go reports (127).
	if tenantSelectionUnavailableExitCode != 128 {
		t.Fatalf("tenantSelectionUnavailableExitCode = %d, want 128", tenantSelectionUnavailableExitCode)
	}
}

func TestBootstrapInitExitErrorStillReportsAMissingRepository(t *testing.T) {
	err := bootstrapInitExitError(fmt.Errorf("outer: %w", common.ErrNotInGitRepository))
	if !errors.Is(err, common.ErrNotInGitRepository) || !internal.IsReported(err) {
		t.Fatalf("missing-repository error lost its reported marking: %v", err)
	}
}
