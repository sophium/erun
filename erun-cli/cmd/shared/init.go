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

// ConfirmPromptTo renders the confirm to out when non-nil; --no-shell routes it to
// stderr so it cannot interleave with the eval-able script on stdout.
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
