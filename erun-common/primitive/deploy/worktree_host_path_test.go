package deploy

import (
	"testing"
)

// The k3s node runs in WSL2 and helm --set mangles backslashes, so the mount
// must be /mnt/<drive>; the deploy goldens normalize this away.
func TestWindowsDrivePathToWSLMount(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"drive path with backslashes", `C:\Users\me\git\erun`, "/mnt/c/Users/me/git/erun"},
		{"lowercase drive letter", `d:\repo`, "/mnt/d/repo"},
		{"already forward-slashed drive path", "C:/Users/me/repo", "/mnt/c/Users/me/repo"},
		{"drive root only", `C:\`, "/mnt/c"},
		{"posix path passes through unchanged", "/home/erun/git/erun", "/home/erun/git/erun"},
	}
	for _, c := range cases {
		if got := WindowsDrivePathToWSLMount(c.in); got != c.want {
			t.Errorf("%s: windowsDrivePathToWSLMount(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
