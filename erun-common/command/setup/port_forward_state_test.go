package setup

import (
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
)

// redirectConfigHomeForTest needs xdg.Reload because adrg/xdg caches ConfigHome
// at init. Cache and AppData roots are redirected too, since forward removal
// also clears legacy locations.
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
