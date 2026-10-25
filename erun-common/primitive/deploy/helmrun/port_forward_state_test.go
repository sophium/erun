package helmrun

import (
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
)

// redirectConfigHomeForTest redirects the config store to a temp root. xdg caches
// ConfigHome at init, so xdg.Reload is required; the cache and AppData roots are
// redirected too because forward removal clears legacy locations there.
func redirectConfigHomeForTest(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("HOME", root)
	t.Setenv("AppData", filepath.Join(root, "AppData", "Roaming"))
	t.Setenv("LocalAppData", filepath.Join(root, "AppData", "Local"))
	xdg.Reload()
	t.Cleanup(xdg.Reload)
}
