package shared

import (
	"io"
	"os"
	"runtime"

	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

type (
	PromptRunner             func(promptui.Prompt) (string, error)
	SelectRunner             func(promptui.Select) (int, string, error)
	KubernetesContextsLister func() ([]string, error)
	MCPLauncher              func(io.Reader, io.Writer, io.Writer, []string) error
	APILauncher              func(io.Reader, io.Writer, io.Writer, []string) error
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

func WithCloudContextPreflight(ctx core.Context, store any) core.Context {
	cloudStore, ok := store.(cloud.CloudContextStore)
	if !ok {
		return ctx
	}
	ctx.KubernetesContextPreflight = cloud.CloudContextPreflight(cloudStore, cloud.CloudContextDependencies{})
	return ctx
}
