package cmd

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
)

// plainPromptInput is the shared line reader for plain-mode prompts. A single
// reader is deliberate: each bufio.Reader buffers ahead, so constructing one
// per prompt would drop the piped input lines a later prompt in the same run
// needs — the same read-ahead that limits piped promptui runs to one prompt
// per process.
var plainPromptInput = sync.OnceValue(func() *bufio.Reader {
	return bufio.NewReader(os.Stdin)
})

// writerIsTerminal reports whether the writer is a real character device.
// ERUN_FORCE_TTY=1 is a deliberate test seam (mirroring ERUN_HOST_OS_OVERRIDE)
// so non-TTY harnesses can opt into the terminal branch.
func writerIsTerminal(w io.Writer) bool {
	if os.Getenv("ERUN_FORCE_TTY") == "1" {
		return true
	}
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}

// runPlainPrompt is the non-TTY fallback for promptui.Prompt: a plain
// fmt-rendered label plus a buffered line read, no cursor-control escapes.
// Pipes get one deterministic line per prompt, which keeps scripted use and
// the integration goldens stable. Semantics mirror promptui: an empty
// line submits the default, Validate re-prompts, and IsConfirm returns
// promptui.ErrAbort unless the answer (or a "y" default met by an empty line)
// confirms.
func runPlainPrompt(prompt promptui.Prompt) (string, error) {
	reader := plainPromptInput()
	for {
		fmt.Print(plainPromptLabel(prompt))
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
				fmt.Println(err.Error())
				continue
			}
		}
		if prompt.IsConfirm && !plainConfirmAccepted(input, prompt.Default) {
			return input, promptui.ErrAbort
		}
		return input, nil
	}
}

// plainConfirmAccepted mirrors promptui's IsConfirm rule: confirmed when the
// answer is "y" (any case), or when the default is "y" and the answer is
// empty.
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

// renderPlainPromptTemplate renders a custom prompt template with promptui's
// style functions stripped to identity, so a templated prompt (e.g. the
// init confirm's "? <label>? [Y/n] ") keeps its wording in plain mode without
// emitting any escape sequences. Returns ok=false when there is no custom
// template or it does not render, letting the caller fall back to the default
// plain label.
func renderPlainPromptTemplate(prompt promptui.Prompt) (string, bool) {
	if prompt.Templates == nil || strings.TrimSpace(prompt.Templates.Prompt) == "" {
		return "", false
	}
	identity := func(value any) string { return fmt.Sprintf("%v", value) }
	parsed, err := template.New("plain-prompt").Funcs(template.FuncMap{
		"black": identity, "red": identity, "green": identity, "yellow": identity,
		"blue": identity, "magenta": identity, "cyan": identity, "white": identity,
		"bgBlack": identity, "bgRed": identity, "bgGreen": identity, "bgYellow": identity,
		"bgBlue": identity, "bgMagenta": identity, "bgCyan": identity, "bgWhite": identity,
		"bold": identity, "faint": identity, "italic": identity, "underline": identity,
	}).Parse(prompt.Templates.Prompt)
	if err != nil {
		return "", false
	}
	var rendered strings.Builder
	if err := parsed.Execute(&rendered, prompt.Label); err != nil {
		return "", false
	}
	return rendered.String(), true
}

// runPlainSelect is the non-TTY fallback for promptui.Select: the label, a
// numbered option list, and a buffered line read. An empty line picks the
// cursor's starting option (the first, matching a piped "\r" confirm against
// promptui); a number or an exact option text picks that option; anything
// else re-prompts.
func runPlainSelect(prompt promptui.Select) (int, string, error) {
	items := plainSelectItems(prompt)
	if len(items) == 0 {
		return 0, "", errors.New("select prompt has no options")
	}
	defaultIndex := prompt.CursorPos
	if defaultIndex < 0 || defaultIndex >= len(items) {
		defaultIndex = 0
	}
	reader := plainPromptInput()
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

// plainSelectChoice resolves one input line against the option list: a number
// within range picks that option, an exact option text picks it, anything
// else does not resolve.
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

// readPlainPromptLine reads one line, tolerating a final unterminated line
// (piped input often ends without a trailing newline, or with a bare "\r"
// written for promptui's enter key). A clean EOF with no content maps to
// promptui.ErrEOF so callers keep one error vocabulary across both prompt
// modes.
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
