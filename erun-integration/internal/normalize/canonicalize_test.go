package normalize

import "testing"

// TestCanonicalizeWindowsPaths locks the guarantee that goldens are OS-invariant:
// a Windows-shaped trace line canonicalizes to the exact Unix shape the goldens
// are recorded in. It drives the transform directly so it runs on every OS.
func TestCanonicalizeWindowsPaths(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			name: "quoted shell-safe path arg loses the Windows-only quotes",
			in:   `--set-string 'worktreeHostPath=<TMP>'`,
			want: `--set-string worktreeHostPath=<TMP>`,
		},
		{
			name: "backslashes in a token-rooted path become forward slashes",
			in:   `trace-log: appending to <HOME>\.erun\team\dev\trace.log`,
			want: `trace-log: appending to <HOME>/.erun/team/dev/trace.log`,
		},
		{
			name: "bare quoted token unquotes",
			in:   `cd '<TMP>' && helm upgrade`,
			want: `cd <TMP> && helm upgrade`,
		},
		{
			name: "quoted path tail canonicalizes and unquotes together",
			in:   `-f '<TMP>\values.yaml'`,
			want: `-f <TMP>/values.yaml`,
		},
		{
			name: "non-shell-safe quoted arg (JSON) stays quoted on both OSes",
			in:   `--set-json 'containerRegistries=[{"registry":"x/y","roles":["build"]}]'`,
			want: `--set-json 'containerRegistries=[{"registry":"x/y","roles":["build"]}]'`,
		},
	}
	for _, c := range cases {
		if got := canonicalizeWindowsPaths(c.in); got != c.want {
			t.Errorf("%s:\ncanonicalizeWindowsPaths(%q) =\n  %q\nwant\n  %q", c.name, c.in, got, c.want)
		}
	}
}
