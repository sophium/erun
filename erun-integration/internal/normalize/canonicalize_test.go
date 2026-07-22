package normalize

import "testing"

// TestForwardSlashWindowsPaths locks the guarantee that goldens are OS-invariant:
// Windows backslash path separators canonicalize to the forward-slash shape the
// goldens are recorded in, while a non-separator backslash (JSON's \") is left
// alone. Drives the transform directly so it runs on every OS.
func TestForwardSlashWindowsPaths(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"remote path arg", `worktreeHostPath=\nonexistent-remote\team`, `worktreeHostPath=/nonexistent-remote/team`},
		{"linux abs path mangled by filepath", `\home\erun\git\team`, `/home/erun/git/team`},
		{"windows temp path keeps drive, slashes only", `C:\Users\x\AppData\Local\Temp\T\v.yaml`, `C:/Users/x/AppData/Local/Temp/T/v.yaml`},
		{"json escaped quote is not a separator", `{"a":"b\"c"}`, `{"a":"b\"c"}`},
	}
	for _, c := range cases {
		if got := forwardSlashWindowsPaths(c.in); got != c.want {
			t.Errorf("%s: forwardSlashWindowsPaths(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestShellSafeQuotedArgStrip locks that the quotes Windows adds around a
// now-shell-safe path arg are dropped (Unix leaves such args bare), while a
// quoted arg with non-shell-safe content (JSON) stays quoted on both OSes.
func TestShellSafeQuotedArgStrip(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"shell-safe path arg unquotes", `--set-string 'worktreeHostPath=/nonexistent-remote/team'`, `--set-string worktreeHostPath=/nonexistent-remote/team`},
		{"bare token unquotes", `cd '<TMP>' && helm`, `cd <TMP> && helm`},
		{"json arg stays quoted", `--set-json 'containerRegistries=[{"registry":"x/y"}]'`, `--set-json 'containerRegistries=[{"registry":"x/y"}]'`},
	}
	for _, c := range cases {
		if got := shellSafeQuotedArg.ReplaceAllString(c.in, "$1"); got != c.want {
			t.Errorf("%s: strip(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
