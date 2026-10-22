package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/manifoldco/promptui"
	"golang.org/x/term"
)

// runConfirmPrompt answers a confirmation.
//
// Where runPrompt would drive promptui's repaint prompt, this reads one
// line-oriented answer instead. promptui's Prompt.Run renders from the caller's
// goroutine while readline's own ioloop renders from another: on Enter the
// ioloop hands the line to Readline -- waking the caller -- and then calls the
// prompt's listener in that same iteration, so the caller's read of the cursor
// and its inputErr and the listener's cursor update always overlap. The two
// share one screenbuf.ScreenBuf and one Cursor, so the race detector reports it
// on every repaint prompt; and a render whose tail is lost takes the settled
// line with it, leaving a frame that carries neither the cursor glyph nor the
// "[Y/n]" marker that normalize.PromptConfirm recognizes, so a stray line
// reaches the golden.
//
// A confirmation needs no repainting: it is one keystroke and the transaction
// is over. Reading it as a line removes the second renderer outright rather
// than serializing it, so there is no shared ScreenBuf or Cursor left to race
// over. Everywhere else the prompt takes the ordinary path -- the injected
// runner, which in production is runPrompt's plain renderer, already one line
// with no repaint.
func runConfirmPrompt(run PromptRunner, prompt promptui.Prompt) (string, error) {
	if promptRepaints(prompt) {
		return runSettledPrompt(prompt)
	}
	return run(prompt)
}

// runSettledPrompt reads one answer and prints the same line the repaint prompt
// settled on: the label once (with its "[Y/n]" hint), then the answered
// confirmation. The settled line is suppressed when the operator's own Enter
// will already have terminated it -- see promptAnswerEchoed -- so a terminal is
// left showing exactly the one line the repaint prompt left there.
//
// The read is a plain one over the shared stdin reader, so the terminal stays
// in canonical mode: the accepted answers, the validation, and the edit keys
// are unchanged, but an interrupt at this prompt now ends the process the way
// it does at any other stdin read instead of arriving as ErrInterrupt from
// readline's own key handling.
func runSettledPrompt(prompt promptui.Prompt) (string, error) {
	writer := promptOutputWriter(prompt)
	reader := plainPromptInput()
	for {
		_, _ = fmt.Fprint(writer, settledPromptLabel(prompt))
		line, err := readPlainPromptLine(reader)
		if err != nil {
			return "", err
		}
		input := strings.TrimSpace(line)
		if input == "" {
			input = prompt.Default
		}
		if prompt.Validate != nil {
			if err := prompt.Validate(input); err != nil {
				_, _ = fmt.Fprintln(writer, err.Error())
				continue
			}
		}
		if !promptAnswerEchoed(prompt, writer) {
			_, _ = fmt.Fprintf(writer, "\n%s\n", settledPromptEcho(prompt)+input)
		}
		return input, nil
	}
}

// settledPromptLabel is the prompt's own label template rendered with promptui's
// styles, so the question reads exactly as the repaint prompt drew it. A prompt
// with no usable template falls back to the plain label rather than printing
// nothing.
func settledPromptLabel(prompt promptui.Prompt) string {
	if prompt.Templates != nil {
		if rendered, ok := renderPromptTemplate(prompt.Templates.Prompt, prompt.Label, promptui.FuncMap); ok {
			return rendered
		}
	}
	return plainPromptLabel(prompt)
}

// settledPromptEcho is the already-answered confirmation, rendered from the
// prompt's Success template as promptui paints it once the value is accepted.
// Without one, the label plus promptui's own default Success suffix keeps the
// settled line readable.
func settledPromptEcho(prompt promptui.Prompt) string {
	if prompt.Templates != nil {
		if rendered, ok := renderPromptTemplate(prompt.Templates.Success, prompt.Label, promptui.FuncMap); ok {
			return rendered
		}
	}
	return fmt.Sprintf("%v", prompt.Label) + ": "
}

// promptOutputWriter is the stream a prompt renders to: the one it was built
// with, or the process's stdout, which is what promptui falls back to.
func promptOutputWriter(prompt promptui.Prompt) io.Writer {
	if prompt.Stdout != nil {
		return prompt.Stdout
	}
	return os.Stdout
}

// promptAnswerEchoed reports whether the operator's Enter will already have
// terminated the label line in this writer. That holds only when the answer is
// read from a terminal that is also the writer: the terminal echoes the
// keystrokes and the newline into that same stream, and a second settled line
// would print the confirmation twice.
//
// It asks about the real terminal rather than reusing writerIsTerminal, whose
// ERUN_FORCE_TTY seam stands in for a terminal the *output* is attached to. A
// forced-TTY run still reads its answer from a pipe, where nothing echoes it.
func promptAnswerEchoed(prompt promptui.Prompt, writer io.Writer) bool {
	source := prompt.Stdin
	if source == nil {
		source = os.Stdin
	}
	stdin, ok := source.(*os.File)
	if !ok || !term.IsTerminal(int(stdin.Fd())) {
		return false
	}
	stdout, ok := writerFile(writer)
	return ok && term.IsTerminal(int(stdout.Fd()))
}

// unwrappingWriter is a writer that only adapts another writer. The alias
// confirm hands its prompt the nopWriteCloser promptStderr builds -- promptui
// takes an io.WriteCloser and must not be able to close the process's real
// stderr -- so the writer promptAnswerEchoed is handed is an adapter, not the
// stream the bytes land on.
type unwrappingWriter interface{ UnwrapWriter() io.Writer }

// writerFile resolves w to the *os.File its bytes land on, following such
// adapters. Without this the alias confirm's writer is never recognized as a
// terminal, so an answer the terminal has already echoed is written a second
// time and the prompt settles on three lines instead of one.
func writerFile(w io.Writer) (*os.File, bool) {
	for {
		if file, ok := w.(*os.File); ok {
			return file, true
		}
		adapter, ok := w.(unwrappingWriter)
		if !ok {
			return nil, false
		}
		w = adapter.UnwrapWriter()
	}
}
