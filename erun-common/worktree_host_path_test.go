package eruncommon

import "testing"

// TestWindowsDrivePathToWSLMount locks the Windows-drive → WSL2 mount translation
// a local-agent env's host worktree needs. The local k3s node runs in WSL2, which
// exposes C:\ under /mnt/c, and helm --set mangles backslashes, so the mount path
// must be the forward-slashed /mnt/<drive> view. The integration deploy goldens
// exercise this path but normalize the result to <TMP>, so they cannot pin the
// concrete translation; this does, on every host.
func TestWindowsDrivePathToWSLMount(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"drive path with backslashes", `C:\Users\me\git\erun`, "/mnt/c/Users/me/git/erun"},
		{"lowercase drive letter", `d:\repo`, "/mnt/d/repo"},
		{"already forward-slashed drive path", "C:/Users/me/repo", "/mnt/c/Users/me/repo"},
		{"drive root only", `C:\`, "/mnt/c"},
		{"posix path passes through unchanged", "/home/erun/git/erun", "/home/erun/git/erun"},
	}
	for _, c := range cases {
		if got := windowsDrivePathToWSLMount(c.in); got != c.want {
			t.Errorf("%s: windowsDrivePathToWSLMount(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
