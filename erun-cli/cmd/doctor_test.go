package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
)

// forceStdinTerminal drives the stdinIsTerminal seam for one test. It is the
// same seam open.go uses, so a run that cannot reach a terminal is exercised
// without depending on how the test process's own stdin happens to be wired.
func forceStdinTerminal(t *testing.T, terminal bool) {
	t.Helper()
	previous := stdinIsTerminal
	stdinIsTerminal = func() bool { return terminal }
	t.Cleanup(func() { stdinIsTerminal = previous })
}

func doctorActionResult() common.OpenResult {
	return common.OpenResult{Tenant: "team", Environment: "dev"}
}

// Without a terminal nobody can answer the optional prune prompts, so doctor
// must skip them, name each one as skipped, and return no error -- a missing
// answer is not a diagnosis. Reporting a failure here is the bug: doctor would
// exit non-zero and read as "this environment is broken" to the orchestrator or
// CI job that needed the report.
func TestDoctorSkipsOptionalPrunePromptsWithoutTerminal(t *testing.T) {
	forceStdinTerminal(t, false)
	prompted := false
	runner := func(promptui.Prompt) (string, error) {
		prompted = true
		return "", promptui.ErrEOF
	}
	var out bytes.Buffer
	ctx := common.Context{Stdout: &out}

	actions, err := selectedDoctorActions(ctx, runner, doctorActionResult(), doctorOptions{}, false)
	if err != nil {
		t.Fatalf("no-TTY doctor returned an error, which reads as a failed environment: %v", err)
	}
	if prompted {
		t.Fatal("no-TTY doctor prompted; the run would block on EOF instead of reporting")
	}
	if len(actions) != 0 {
		t.Fatalf("no-TTY doctor selected %v; an unanswered optional action must not run", actions)
	}

	report := out.String()
	for _, action := range common.DoctorActions() {
		if !strings.Contains(report, "skipped: "+string(action)) {
			t.Errorf("report does not name %s as skipped:\n%s", action, report)
		}
	}
	if !strings.Contains(report, "not failed checks") {
		t.Errorf("report does not distinguish a skipped optional step from a failed check:\n%s", report)
	}
	for _, flag := range []string{"--prune-images", "--prune-build-cache", "--prune-containers"} {
		if !strings.Contains(report, flag) {
			t.Errorf("report does not offer %s as the explicit way to run it:\n%s", flag, report)
		}
	}
}

// A real terminal still gets the prompts: the skip is a no-TTY fallback, not a
// removal of the interactive flow.
func TestDoctorPromptsForOptionalPrunesOnTerminal(t *testing.T) {
	forceStdinTerminal(t, true)
	var asked []string
	runner := func(prompt promptui.Prompt) (string, error) {
		asked = append(asked, fmt.Sprintf("%v", prompt.Label))
		return "y", nil
	}
	var out bytes.Buffer
	ctx := common.Context{Stdout: &out}

	actions, err := selectedDoctorActions(ctx, runner, doctorActionResult(), doctorOptions{}, false)
	if err != nil {
		t.Fatalf("terminal doctor failed: %v", err)
	}
	want := common.DoctorActions()
	if len(actions) != len(want) {
		t.Fatalf("answered prompts selected %v, want %v", actions, want)
	}
	for i, action := range want {
		if actions[i] != action {
			t.Errorf("action %d = %s, want %s", i, actions[i], action)
		}
		// confirmPrompt trims the trailing "?" before handing the label to the
		// prompt, which supplies its own.
		wantLabel := strings.TrimRight(common.DoctorActionPromptLabel(action, doctorActionResult()), "?")
		if !strings.Contains(asked[i], wantLabel) {
			t.Errorf("prompt %d = %q, want it to offer %q", i, asked[i], wantLabel)
		}
	}
	if strings.Contains(out.String(), "skipped") {
		t.Errorf("terminal run reported skipped prompts:\n%s", out.String())
	}
}

// A terminal that closes mid-run (EOF) is still nobody declining an optional
// action, not a verdict on the environment.
func TestDoctorTreatsPromptEOFAsSkippedNotFailed(t *testing.T) {
	forceStdinTerminal(t, true)
	runner := func(promptui.Prompt) (string, error) { return "", promptui.ErrEOF }
	var out bytes.Buffer
	ctx := common.Context{Stdout: &out}

	actions, err := selectedDoctorActions(ctx, runner, doctorActionResult(), doctorOptions{}, false)
	if err != nil {
		t.Fatalf("EOF from an optional prompt failed doctor: %v", err)
	}
	if len(actions) != 0 {
		t.Fatalf("EOF selected %v, want nothing selected", actions)
	}
	if !strings.Contains(out.String(), "skipped: "+string(common.DoctorActionPruneImages)) {
		t.Errorf("EOF was not reported as a skipped optional step:\n%s", out.String())
	}
	// The cause differs from a run that never had a terminal, and the report
	// must name the one that happened instead of prescribing the wrong fix.
	if !strings.Contains(out.String(), doctorPromptsStdinClosed) {
		t.Errorf("EOF was reported with the wrong cause:\n%s", out.String())
	}
	if strings.Contains(out.String(), doctorPromptsNoTerminal) {
		t.Errorf("a terminal that closed was reported as no terminal at all:\n%s", out.String())
	}
}

// The invariant the reported bug violated: no prompt doctor cannot skip may
// turn an absent answer into "Doctor failed". A step that mutates the live
// release is not run without consent, but the caller still gets the diagnosis
// and the flag that would run it.
func TestDoctorConfirmDeclinesOnEOFWithoutFailing(t *testing.T) {
	runner := func(promptui.Prompt) (string, error) { return "", promptui.ErrEOF }
	var out bytes.Buffer
	ctx := common.Context{Stdout: &out}

	confirmed, err := doctorConfirm(ctx, runner, "Roll back team/dev to its last successful revision?", "Re-run with --rollback to run it without a prompt.")
	if err != nil {
		t.Fatalf("EOF from a doctor confirm failed the run: %v", err)
	}
	if confirmed {
		t.Fatal("EOF was taken as consent to mutate the live release")
	}
	if !strings.Contains(out.String(), "--rollback") {
		t.Errorf("declined step did not name the flag that runs it without a prompt:\n%s", out.String())
	}
}

// An error that is not EOF still fails: a genuinely broken prompt runner is a
// real failure, and must not be laundered into a quiet skip.
func TestDoctorStillReportsRealPromptErrors(t *testing.T) {
	forceStdinTerminal(t, true)
	runner := func(promptui.Prompt) (string, error) { return "", errors.New("prompt runner broke") }
	ctx := common.Context{Stdout: &bytes.Buffer{}}

	if _, err := selectedDoctorActions(ctx, runner, doctorActionResult(), doctorOptions{}, false); err == nil {
		t.Fatal("a real prompt-runner error was swallowed")
	}
}
