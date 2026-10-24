package prompt

import (
	"os"
	"runtime"

	"github.com/manifoldco/promptui"
)

type (
	PromptRunner             func(promptui.Prompt) (string, error)
	SelectRunner             func(promptui.Select) (int, string, error)
	KubernetesContextsLister func() ([]string, error)
)

// RunPrompt falls back to plain prompts on a piped stdout (promptui's goroutine
// repaints race exit) and on Windows (ConPTY duplicates repaints); masked prompts stay
// on promptui. Its reader is os.Stdin so piped input works on Windows too.
func RunPrompt(prompt promptui.Prompt) (string, error) {
	if promptRepaints(prompt) {
		prompt.Stdin = os.Stdin
		return prompt.Run()
	}
	return runPlainPrompt(prompt)
}

// promptRepaints reports whether RunPrompt would hand this prompt to promptui's repainting renderer.
func promptRepaints(prompt promptui.Prompt) bool {
	return WriterIsTerminal(os.Stdout) && (runtime.GOOS != "windows" || prompt.Mask != 0)
}
