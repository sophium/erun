package open

import (
	"bufio"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/cmd/shared"
)

var confirmANSI = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// confirmPromptStdin points both promptui's os.Stdin and the shared, once-bound plain reader at one pipe.
func confirmPromptStdin(t *testing.T, answer string, rounds int) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := io.WriteString(writer, strings.Repeat(answer+"\n", rounds)); err != nil {
		t.Fatalf("seed stdin: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close stdin writer: %v", err)
	}
	previousStdin, previousInput := os.Stdin, shared.PlainPromptInput
	os.Stdin = reader
	// One reader for the run; a fresh bufio.Reader per prompt would buffer later answers away.
	shared.PlainPromptInput = sync.OnceValue(func() *bufio.Reader { return bufio.NewReader(reader) })
	t.Cleanup(func() {
		os.Stdin, shared.PlainPromptInput = previousStdin, previousInput
		_ = reader.Close()
	})
}

// Repeating the confirmation exposes promptui's repaint race to -race and lets
// renders be compared.
func TestConfirmPromptSettlesOnOneRecognizableLine(t *testing.T) {
	t.Setenv("ERUN_FORCE_TTY", "1")
	const rounds = 20
	confirmPromptStdin(t, "y", rounds)

	out, err := os.CreateTemp(t.TempDir(), "confirm")
	if err != nil {
		t.Fatalf("capture file: %v", err)
	}
	defer func() { _ = out.Close() }()
	writer := nopWriteCloser{out}

	consumed := 0
	for round := 0; round < rounds; round++ {
		ok, err := shared.ConfirmPromptTo(shared.RunPrompt, "add team-dev to ~/.bashrc", writer)
		if err != nil {
			t.Fatalf("round %d: confirmPromptTo: %v", round, err)
		}
		if !ok {
			t.Fatalf("round %d: answering y must confirm", round)
		}

		captured, err := os.ReadFile(out.Name())
		if err != nil {
			t.Fatalf("round %d: read capture: %v", round, err)
		}
		delta := string(captured[consumed:])
		consumed = len(captured)
		renders := strings.Split(strings.TrimRight(confirmANSI.ReplaceAllString(strings.ReplaceAll(delta, "\r", ""), ""), "\n"), "\n")
		if len(renders) != 2 {
			t.Fatalf("round %d: the confirm rendered %d lines, want 2 (a label and its settled line), so a capture cannot keep just the answered one:\n%q",
				round, len(renders), delta)
		}
		// The golden's normalizer drops exactly this line.
		if !strings.Contains(renders[0], "[Y/n]") {
			t.Fatalf("round %d: label %q carries no [Y/n] marker, so a capture cannot tell it apart from an answer", round, renders[0])
		}
		if want := "add team-dev to ~/.bashrc? y"; renders[1] != want {
			t.Fatalf("round %d: settled line %q, want %q", round, renders[1], want)
		}
	}
}

func TestConfirmPromptReachesTheInjectedRunnerWithoutATerminal(t *testing.T) {
	t.Setenv("ERUN_FORCE_TTY", "")
	confirmPromptStdin(t, "y", 1)

	var shown promptui.Prompt
	runner := func(prompt promptui.Prompt) (string, error) {
		shown = prompt
		return "y", nil
	}
	ok, err := shared.ConfirmPromptTo(runner, "add team-dev to ~/.bashrc", nopWriteCloser{io.Discard})
	if err != nil {
		t.Fatalf("confirmPromptTo: %v", err)
	}
	if !ok {
		t.Fatal("answering y must confirm")
	}
	if want := "add team-dev to ~/.bashrc"; shown.Label != want {
		t.Fatalf("injected runner saw label %q, want %q", shown.Label, want)
	}
}
