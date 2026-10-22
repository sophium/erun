package cmd

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/manifoldco/promptui"
)

// confirmANSI mirrors the integration suite's first normalization rule, which
// strips escape codes before anything compares a capture.
var confirmANSI = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// confirmPromptStdin points both readers a confirmation can use at one pipe:
// the prompt's own os.Stdin (promptui reads it directly) and the shared plain
// reader, which is a sync.OnceValue already bound to whatever os.Stdin was when
// another test first prompted.
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
	previousStdin, previousInput := os.Stdin, plainPromptInput
	os.Stdin = reader
	// One reader for the whole run, the way production binds it: a fresh
	// bufio.Reader per prompt would buffer the later answers away.
	plainPromptInput = sync.OnceValue(func() *bufio.Reader { return bufio.NewReader(reader) })
	t.Cleanup(func() {
		os.Stdin, plainPromptInput = previousStdin, previousInput
		_ = reader.Close()
	})
}

// The confirmation the open --no-shell alias flow asks must settle on one
// recognizable line, every time.
//
// Its capture is compared through normalize.PromptConfirm, which recognizes a
// repaint frame by the cursor glyph or the "[Y/n]" marker and keeps the one
// settled line. promptui renders that frame from a readline goroutine while the
// caller renders from its own, over one shared ScreenBuf and Cursor: `go test
// -race` reports that overlap on every repaint prompt (Cursor.Get vs
// Cursor.Update, and the Run loop's inputErr write vs the listener's read), and
// a frame whose tail is lost leaves a line carrying neither marker, so it
// survives normalization and reds the golden on content that passed in another
// run. Driving the confirmation many times in one process is what makes both
// visible here: the race detector sees the overlap, and a renderer that
// repaints cannot produce the same two lines twice.
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
		ok, err := confirmPromptTo(runPrompt, "add team-dev to ~/.bashrc", writer)
		if err != nil {
			t.Fatalf("round %d: confirmPromptTo: %v", round, err)
		}
		if !ok {
			t.Fatalf("round %d: answering y must confirm", round)
		}

		// One confirmation's render, read back from the capture and normalized
		// the way the golden's does: strip escape codes and carriage returns,
		// then read the lines.
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
		// The label has to stay recognizable as a waiting prompt: the golden's
		// normalizer drops exactly this line.
		if !strings.Contains(renders[0], "[Y/n]") {
			t.Fatalf("round %d: label %q carries no [Y/n] marker, so a capture cannot tell it apart from an answer", round, renders[0])
		}
		if want := "add team-dev to ~/.bashrc? y"; renders[1] != want {
			t.Fatalf("round %d: settled line %q, want %q", round, renders[1], want)
		}
	}
}

// Without a terminal the confirmation keeps the plain render it has always had:
// the prompt's own runner answers it, and the label is printed once with no
// settled line after it. That exact shape is what the init, cloud and doctor
// goldens compare.
func TestConfirmPromptWithoutTerminalKeepsThePlainRender(t *testing.T) {
	t.Setenv("ERUN_FORCE_TTY", "")
	confirmPromptStdin(t, "y", 1)

	var out bytes.Buffer
	ok, err := confirmPromptTo(runPrompt, "add team-dev to ~/.bashrc", nopWriteCloser{&out})
	if err != nil {
		t.Fatalf("confirmPromptTo: %v", err)
	}
	if !ok {
		t.Fatal("answering y must confirm")
	}
	if want := "? add team-dev to ~/.bashrc? [Y/n] "; out.String() != want {
		t.Fatalf("plain confirm rendered %q, want %q", out.String(), want)
	}
}

// The injected runner is the seam tests answer prompts through, so a
// confirmation must keep reaching it wherever the repaint prompt is not in
// play.
func TestConfirmPromptReachesTheInjectedRunnerWithoutATerminal(t *testing.T) {
	t.Setenv("ERUN_FORCE_TTY", "")
	confirmPromptStdin(t, "y", 1)

	var shown promptui.Prompt
	runner := func(prompt promptui.Prompt) (string, error) {
		shown = prompt
		return "y", nil
	}
	ok, err := confirmPromptTo(runner, "add team-dev to ~/.bashrc", nopWriteCloser{io.Discard})
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
