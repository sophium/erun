package prompt

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"text/template"

	"github.com/manifoldco/promptui"
	"golang.org/x/term"
)

// One shared reader: per-prompt bufio.Readers would buffer ahead and drop later piped answers.
var PlainPromptInput = sync.OnceValue(func() *bufio.Reader {
	return bufio.NewReader(os.Stdin)
})

// WriterIsTerminal reports whether w is a real terminal; ModeCharDevice alone also matches /dev/null.
func WriterIsTerminal(w io.Writer) bool {
	if os.Getenv("ERUN_FORCE_TTY") == "1" {
		return true
	}
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

// runPlainPrompt is the non-TTY fallback for promptui.Prompt. It writes to the prompt's
// own stream so a prompt routed off stdout stays off the eval-able script.
func runPlainPrompt(prompt promptui.Prompt) (string, error) {
	writer := promptOutputWriter(prompt)
	reader := PlainPromptInput()
	for {
		_, _ = fmt.Fprint(writer, plainPromptLabel(prompt))
		line, err := readPlainPromptLine(reader)
		if err != nil {
			return "", err
		}
		input := strings.TrimSpace(line)
		if input == "" && !prompt.IsConfirm {
			input = prompt.Default
		}
		if prompt.Validate != nil {
			if err := prompt.Validate(input); err != nil {
				_, _ = fmt.Fprintln(writer, err.Error())
				continue
			}
		}
		if prompt.IsConfirm && !plainConfirmAccepted(input, prompt.Default) {
			return input, promptui.ErrAbort
		}
		return input, nil
	}
}

func plainConfirmAccepted(input, defaultValue string) bool {
	if strings.EqualFold(input, "y") {
		return true
	}
	return input == "" && strings.EqualFold(defaultValue, "y")
}

func plainPromptLabel(prompt promptui.Prompt) string {
	if rendered, ok := renderPlainPromptTemplate(prompt); ok {
		return rendered
	}
	label := fmt.Sprintf("%v", prompt.Label)
	if prompt.IsConfirm {
		hint := "[y/N]"
		if strings.EqualFold(prompt.Default, "y") {
			hint = "[Y/n]"
		}
		return label + "? " + hint + " "
	}
	if strings.TrimSpace(prompt.Default) != "" {
		return label + " [" + prompt.Default + "]: "
	}
	return label + ": "
}

// renderPlainPromptTemplate renders the custom template without escape sequences; ok=false means none usable.
func renderPlainPromptTemplate(prompt promptui.Prompt) (string, bool) {
	if prompt.Templates == nil {
		return "", false
	}
	return renderPromptTemplate(prompt.Templates.Prompt, prompt.Label, plainPromptFuncs())
}

// renderPromptTemplate renders one of a prompt's templates with the given style funcs; ok=false if unusable.
func renderPromptTemplate(text string, label any, funcs template.FuncMap) (string, bool) {
	if strings.TrimSpace(text) == "" {
		return "", false
	}
	parsed, err := template.New("prompt").Funcs(funcs).Parse(text)
	if err != nil {
		return "", false
	}
	var rendered strings.Builder
	if err := parsed.Execute(&rendered, label); err != nil {
		return "", false
	}
	return rendered.String(), true
}

// plainPromptFuncs is promptui.FuncMap with every style reduced to identity.
func plainPromptFuncs() template.FuncMap {
	identity := func(value any) string { return fmt.Sprintf("%v", value) }
	funcs := make(template.FuncMap, len(promptui.FuncMap))
	for name := range promptui.FuncMap {
		funcs[name] = identity
	}
	return funcs
}

// RunPlainSelect is the non-TTY fallback for promptui.Select; an empty line picks the cursor's option.
func RunPlainSelect(prompt promptui.Select) (int, string, error) {
	items := plainSelectItems(prompt)
	if len(items) == 0 {
		return 0, "", errors.New("select prompt has no options")
	}
	defaultIndex := prompt.CursorPos
	if defaultIndex < 0 || defaultIndex >= len(items) {
		defaultIndex = 0
	}
	reader := PlainPromptInput()
	fmt.Printf("%v:\n", prompt.Label)
	for i, item := range items {
		fmt.Printf("  %d) %s\n", i+1, item)
	}
	for {
		fmt.Printf("Select 1-%d [%d]: ", len(items), defaultIndex+1)
		line, err := readPlainPromptLine(reader)
		if err != nil {
			return 0, "", err
		}
		input := strings.TrimSpace(line)
		if input == "" {
			return defaultIndex, items[defaultIndex], nil
		}
		if index, ok := plainSelectChoice(input, items); ok {
			return index, items[index], nil
		}
		fmt.Printf("enter a number between 1 and %d\n", len(items))
	}
}

func plainSelectChoice(input string, items []string) (int, bool) {
	if number, err := strconv.Atoi(input); err == nil && number >= 1 && number <= len(items) {
		return number - 1, true
	}
	for i, item := range items {
		if item == input {
			return i, true
		}
	}
	return 0, false
}

func plainSelectItems(prompt promptui.Select) []string {
	value := reflect.ValueOf(prompt.Items)
	if !value.IsValid() || value.Kind() != reflect.Slice {
		return nil
	}
	items := make([]string, 0, value.Len())
	for i := 0; i < value.Len(); i++ {
		items = append(items, fmt.Sprintf("%v", value.Index(i).Interface()))
	}
	return items
}

// readPlainPromptLine accepts an unterminated final line; a clean empty EOF maps to promptui.ErrEOF.
func readPlainPromptLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && line != "" {
			return line, nil
		}
		if errors.Is(err, io.EOF) {
			return "", promptui.ErrEOF
		}
		return "", err
	}
	return line, nil
}
