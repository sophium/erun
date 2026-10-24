package shared

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/manifoldco/promptui"
)

func ConfirmPrompt(run PromptRunner, label string) (bool, error) {
	return ConfirmPromptTo(run, label, nil)
}

// ConfirmPromptTo renders the confirm to out instead of promptui's default
// (os.Stdout) when out is non-nil. The open --no-shell alias flow routes it to
// stderr: the confirm writes while the eval-able setup script is printed to
// stdout immediately afterward, so leaving the prompt on stdout lets the two
// writers interleave non-deterministically (on Windows the prompt frames fused
// into the first script line ~1 run in 20). Keeping the UI on stderr leaves
// stdout carrying only the script.
//
// The answer comes from runConfirmPrompt, which drops promptui's repaint prompt
// for this confirmation (see its comment); the label, the accepted inputs, and
// the empty-answer default below are unchanged.
func ConfirmPromptTo(run PromptRunner, label string, out io.WriteCloser) (bool, error) {
	label = strings.TrimRight(strings.TrimSpace(label), "?")
	prompt := promptui.Prompt{
		Label:  label,
		Stdout: out,
		Templates: &promptui.PromptTemplates{
			Prompt:  `{{ "?" | blue }} {{ . | bold }}? {{ "[Y/n]" | faint }} `,
			Valid:   `{{ "?" | blue }} {{ . | bold }}? {{ "[Y/n]" | faint }} `,
			Invalid: `{{ "?" | blue }} {{ . | bold }}? {{ "[Y/n]" | faint }} `,
			Success: `{{ . | faint }}? `,
		},
		Validate: func(input string) error {
			switch strings.ToLower(strings.TrimSpace(input)) {
			case "", "y", "n":
				return nil
			default:
				return fmt.Errorf("enter y or n")
			}
		},
	}

	result, err := runConfirmPrompt(run, prompt)
	if err != nil {
		if errors.Is(err, promptui.ErrInterrupt) {
			return false, fmt.Errorf("initialization interrupted")
		}
		if errors.Is(err, promptui.ErrAbort) {
			return false, nil
		}
		return false, err
	}

	if result == "" {
		return true, nil
	}

	return strings.EqualFold(strings.TrimSpace(result), "y"), nil
}
