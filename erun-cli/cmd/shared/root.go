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

// A piped stdout falls back to plain prompts because promptui repaints from its
// own goroutines, so its cursor-control frames' final flush would otherwise race
// process exit.
//
// Windows takes the plain reader for interactive terminals too: the desktop app's
// terminal is a ConPTY, whose repaints re-send promptui's in-place cursor redraws
// as duplicate lines, so each answered prompt echoes twice. A single-write plain
// prompt gives ConPTY nothing to re-paint. Masked prompts stay on promptui so the
// secret input remains hidden — keeping it masked matters more than the repaint.
//
// The promptui branch binds its reader to os.Stdin explicitly: on Windows
// readline otherwise opens the console directly and cannot read piped stdin at
// all, so a forced-TTY scenario (or any scripted run) would read EOF. Passing
// os.Stdin is a no-op for a real interactive terminal — that is already the
// default source — and lets piped input drive the prompt on every host.
func RunPrompt(prompt promptui.Prompt) (string, error) {
	if promptRepaints(prompt) {
		prompt.Stdin = os.Stdin
		return prompt.Run()
	}
	return runPlainPrompt(prompt)
}

// promptRepaints reports whether promptui would own this prompt's render, i.e.
// whether runPrompt would call Prompt.Run and so start the readline ioloop that
// repaints concurrently with the caller. A caller that renders the prompt
// itself takes the same decision from here rather than testing the terminal
// again, so the two branches cannot disagree about which renderer a prompt got.
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
