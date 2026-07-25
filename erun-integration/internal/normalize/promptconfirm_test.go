package normalize

import "testing"

// TestPromptConfirmCollapsesRepaintFrames locks that a promptui confirm — once
// it owns its output stream (the open alias flow routes it to stderr so its
// repaints never race the stdout eval script) — collapses to a single settled
// confirm line no matter how many repaint frames readline emitted or whether a
// stray [Y/n] redraw trailed the settled render. The shapes are taken from real
// Windows stderr captures, ANSI escapes and all, and use a POSIX temp path so
// the temp-dir rule normalizes it on every host.
func TestPromptConfirmCollapsesRepaintFrames(t *testing.T) {
	const p = "/private/var/folders/ab/TestOpenalias/001/home/.zshrc"
	const esc = "\x1b"
	// One clean repaint frame still showing the [Y/n] prompt and the █ cursor.
	promptFrame := esc + "[2K\r" + esc + "[34m?" + esc + "[0m " + esc + "[1madd team-dev to " + p + esc + "[0m? " + esc + "[2m[Y/n]" + esc + "[0m n█\n"
	// The settled render promptui leaves after a valid answer: label, no prompt.
	settled := esc + "[1A" + esc + "[2K\r" + esc + "[2K\r" + esc + "[2madd team-dev to " + p + esc + "[0m? n\n"
	const preceding = "open: --no-shell selected, emitting setup commands instead of launching shell\n"
	const hint = "one-liner alias:\nalias team-dev='eval \"$(erun open team dev --no-shell)\"'\n"
	want := preceding + "add team-dev to <TMP> n\n" + hint

	cases := []struct{ name, raw string }{
		{
			"settled render is the last frame",
			preceding + esc + "[?25l" + promptFrame + "n\r\b" + promptFrame + settled + esc + "[?25h" + hint,
		},
		{
			// The flaky variant: a stray prompt redraw trails the settled render.
			// Dropping every [Y/n]/█ frame still leaves the one settled line.
			"stray prompt redraw trails the settled render",
			preceding + esc + "[?25l" + promptFrame + "n\r\b" + settled + promptFrame + esc + "[?25h" + hint,
		},
	}
	for _, c := range cases {
		if got := PromptConfirm(c.raw); got != want {
			t.Errorf("%s:\n got: %q\nwant: %q", c.name, got, want)
		}
	}
}
