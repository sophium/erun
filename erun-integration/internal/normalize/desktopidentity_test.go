package normalize

import "testing"

// TestDesktopIdentityPathIsHostIndependent locks that the desktop identity path
// collapses to one token on every host. It resolves through os.UserConfigDir(),
// which the harness cannot pin the way ERUN_HOST_OS_OVERRIDE pins a code branch:
// darwin returns "$HOME/Library/Application Support" and Linux the XDG dir. The
// generic temp-dir rule stops at that space, so before this rule the scenario
// recorded one golden on darwin and a different one on Linux — and whichever
// host did not record it went red. Drives Apply directly so it runs everywhere.
func TestDesktopIdentityPathIsHostIndependent(t *testing.T) {
	const want = "no desktop identity at <DESKTOP_IDENTITY>; open an environment from the ERun desktop app once\n"
	cases := []struct{ name, path string }{
		{"darwin user config dir", "/private/var/folders/ab/TestMCP/001/Library/Application Support/ERun/desktopid.key"},
		{"linux xdg config dir", "/tmp/TestMCP/001/ERun/desktopid.key"},
		{"macos temp root without the private prefix", "/var/folders/ab/TestMCP/001/Library/Application Support/ERun/desktopid.key"},
	}
	for _, c := range cases {
		raw := "no desktop identity at " + c.path + "; open an environment from the ERun desktop app once\n"
		if got := Apply(raw); got != want {
			t.Errorf("%s:\n got: %q\nwant: %q", c.name, got, want)
		}
	}
}
