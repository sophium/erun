package normalize

import "testing"

// TestForwardSlashWindowsPaths: only drive-letter paths are forward-slashed;
// escape sequences and bare-backslash paths are left alone.
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
