package doctor

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/doctor"
)

func forceStdinTerminal(t *testing.T, terminal bool) {
	t.Helper()
	previous := shared.StdinIsTerminal
	shared.StdinIsTerminal = func() bool { return terminal }
	t.Cleanup(func() { shared.StdinIsTerminal = previous })
}

func doctorActionResult() config.OpenResult {
	return config.OpenResult{Tenant: "team", Environment: "dev"}
}

func TestDoctorPromptsForOptionalPrunesOnTerminal(t *testing.T) {
	forceStdinTerminal(t, true)
	var asked []string
	runner := func(prompt promptui.Prompt) (string, error) {
		asked = append(asked, fmt.Sprintf("%v", prompt.Label))
		return "y", nil
	}
	var out bytes.Buffer
	ctx := core.Context{Stdout: &out}

	actions, err := selectedDoctorActions(ctx, runner, doctorActionResult(), doctorOptions{}, false)
	if err != nil {
		t.Fatalf("terminal doctor failed: %v", err)
	}
	want := doctor.DoctorActions()
	if len(actions) != len(want) {
		t.Fatalf("answered prompts selected %v, want %v", actions, want)
	}
	for i, action := range want {
		if actions[i] != action {
			t.Errorf("action %d = %s, want %s", i, actions[i], action)
		}
		// The prompt supplies its own "?".
		wantLabel := strings.TrimRight(doctor.DoctorActionPromptLabel(action, doctorActionResult()), "?")
		if !strings.Contains(asked[i], wantLabel) {
			t.Errorf("prompt %d = %q, want it to offer %q", i, asked[i], wantLabel)
		}
	}
	if strings.Contains(out.String(), "skipped") {
		t.Errorf("terminal run reported skipped prompts:\n%s", out.String())
	}
}

func TestDoctorTreatsPromptEOFAsSkippedNotFailed(t *testing.T) {
	forceStdinTerminal(t, true)
	runner := func(promptui.Prompt) (string, error) { return "", promptui.ErrEOF }
	var out bytes.Buffer
	ctx := core.Context{Stdout: &out}

	actions, err := selectedDoctorActions(ctx, runner, doctorActionResult(), doctorOptions{}, false)
	if err != nil {
		t.Fatalf("EOF from an optional prompt failed doctor: %v", err)
	}
	if len(actions) != 0 {
		t.Fatalf("EOF selected %v, want nothing selected", actions)
	}
	if !strings.Contains(out.String(), "skipped: "+string(doctor.DoctorActionPruneImages)) {
		t.Errorf("EOF was not reported as a skipped optional step:\n%s", out.String())
	}
	if !strings.Contains(out.String(), doctorPromptsStdinClosed) {
		t.Errorf("EOF was reported with the wrong cause:\n%s", out.String())
	}
	if strings.Contains(out.String(), doctorPromptsNoTerminal) {
		t.Errorf("a terminal that closed was reported as no terminal at all:\n%s", out.String())
	}
}

func TestDoctorStillReportsRealPromptErrors(t *testing.T) {
	forceStdinTerminal(t, true)
	runner := func(promptui.Prompt) (string, error) { return "", errors.New("prompt runner broke") }
	ctx := core.Context{Stdout: &bytes.Buffer{}}

	if _, err := selectedDoctorActions(ctx, runner, doctorActionResult(), doctorOptions{}, false); err == nil {
		t.Fatal("a real prompt-runner error was swallowed")
	}
}
