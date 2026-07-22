package normalize

import "testing"

// TestForwardSlashWindowsPaths locks that only unambiguous drive-letter Windows
// paths are forward-slashed for golden parity, while backslash escape sequences
// in traced script bodies (\n, \033) and bare-backslash paths (fixed at the erun
// source, not here) are left untouched. Drives the transform directly so it runs
// on every OS.
func TestForwardSlashWindowsPaths(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"drive path forward-slashed", `C:\Users\x\AppData\Local\Temp\T\v.yaml`, `C:/Users/x/AppData/Local/Temp/T/v.yaml`},
		{"quoted drive path inner slashed", `-f 'C:\Users\x\repo\values.yaml'`, `-f 'C:/Users/x/repo/values.yaml'`},
		{"escape sequence in a script body is not a separator", `printf '== usage ==\n'`, `printf '== usage ==\n'`},
		{"octal escape left alone", `printf '\033]0;title\007'`, `printf '\033]0;title\007'`},
		{"bare-backslash path is fixed at the source, not here", `mkdir \home\erun\git`, `mkdir \home\erun\git`},
	}
	for _, c := range cases {
		if got := forwardSlashWindowsPaths(c.in); got != c.want {
			t.Errorf("%s: forwardSlashWindowsPaths(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
