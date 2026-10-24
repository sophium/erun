package prompt

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/manifoldco/promptui"
	"golang.org/x/term"
)

// runConfirmPrompt reads a confirmation as one line instead of promptui's repaint
// prompt, whose two renderers race over a shared ScreenBuf and Cursor.
func runConfirmPrompt(run PromptRunner, prompt promptui.Prompt) (string, error) {
	if promptRepaints(prompt) {
		return runSettledPrompt(prompt)
	}
	return run(prompt)
}

// runSettledPrompt reads one answer and prints the line the repaint prompt would
// have settled on, unless the terminal already echoed it.
func runSettledPrompt(prompt promptui.Prompt) (string, error) {
	writer := promptOutputWriter(prompt)
	reader := PlainPromptInput()
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

// settledPromptLabel renders the prompt's label template, falling back to the plain label.
func settledPromptLabel(prompt promptui.Prompt) string {
	if prompt.Templates != nil {
		if rendered, ok := renderPromptTemplate(prompt.Templates.Prompt, prompt.Label, promptui.FuncMap); ok {
			return rendered
		}
	}
	return plainPromptLabel(prompt)
}

// settledPromptEcho renders the prompt's Success template, or label plus promptui's default suffix.
func settledPromptEcho(prompt promptui.Prompt) string {
	if prompt.Templates != nil {
		if rendered, ok := renderPromptTemplate(prompt.Templates.Success, prompt.Label, promptui.FuncMap); ok {
			return rendered
		}
	}
	return fmt.Sprintf("%v", prompt.Label) + ": "
}

func promptOutputWriter(prompt promptui.Prompt) io.Writer {
	if prompt.Stdout != nil {
		return prompt.Stdout
	}
	return os.Stdout
}

// promptAnswerEchoed reports whether the operator's Enter already ended the label line in writer.
// It checks the real terminal, not writerIsTerminal: a forced-TTY run still reads from a pipe.
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

// unwrappingWriter is implemented by writers that only adapt another writer.
type unwrappingWriter interface{ UnwrapWriter() io.Writer }

// writerFile resolves w through adapters to the *os.File its bytes land on.
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
