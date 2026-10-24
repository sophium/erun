package normalize

import "testing"

// TestPromptConfirmCollapsesRepaintFrames: promptui repaint frames, from real
// Windows captures, collapse to the one settled confirm line.
func TestPromptConfirmCollapsesRepaintFrames(t *testing.T) {
	const p = "/private/var/folders/ab/TestOpenalias/001/home/.zshrc"
	const esc = "\x1b"
	promptFrame := esc + "[2K\r" + esc + "[34m?" + esc + "[0m " + esc + "[1madd team-dev to " + p + esc + "[0m? " + esc + "[2m[Y/n]" + esc + "[0m n█\n"
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
			// A stray prompt redraw after the settled render.
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
