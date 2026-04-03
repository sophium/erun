package cmd

import (
	"bytes"
	"errors"
	"testing"

	"github.com/manifoldco/promptui"
)

func TestNewRootCmdRegistersCommands(t *testing.T) {
	cmd := NewRootCmd(Dependencies{})

	for _, name := range []string{"init", "mcp", "version"} {
		found, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("Find(%q) failed: %v", name, err)
		}
		if found == nil || found.Name() != name {
			t.Fatalf("expected command %q to be registered", name)
		}
	}
}

func TestRootCommandPrintsHelp(t *testing.T) {
	cmd := NewRootCmd(Dependencies{})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := buf.String()
	for _, want := range []string{"init", "mcp", "version"} {
		if !bytes.Contains([]byte(output), []byte(want)) {
			t.Fatalf("expected help output to mention %q, got %q", want, output)
		}
	}
}

func TestConfirmPromptDefaultAndErrors(t *testing.T) {
	run := func(result string, err error) PromptRunner {
		return func(prompt promptui.Prompt) (string, error) {
			return result, err
		}
	}

	if ok, err := confirmPrompt(run("", nil), "label"); err != nil || !ok {
		t.Fatalf("expected default confirmation, got %v %v", ok, err)
	}

	if ok, err := confirmPrompt(run("n", nil), "label"); err != nil || ok {
		t.Fatalf("expected rejection, got %v %v", ok, err)
	}

	if ok, err := confirmPrompt(run("", promptui.ErrAbort), "label"); err != nil || ok {
		t.Fatalf("expected abort to be treated as rejection, got %v %v", ok, err)
	}

	if ok, err := confirmPrompt(run("", promptui.ErrInterrupt), "label"); err == nil || ok {
		t.Fatalf("expected interrupt error, got %v %v", ok, err)
	}

	expectedErr := errors.New("boom")
	if ok, err := confirmPrompt(run("", expectedErr), "label"); !errors.Is(err, expectedErr) || ok {
		t.Fatalf("expected original error, got %v %v", ok, err)
	}
}
