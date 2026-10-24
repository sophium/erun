package deploy

import (
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
)

// redirectConfigHomeForTest points the on-disk config store (and therefore
// PortForwardStatePath/ConfigStore) at a fresh temp root. adrg/xdg caches
// ConfigHome at process init rather than re-reading the environment per
// call, so the Setenv calls alone would not redirect it -- xdg.Reload is
// what makes it honour this test's temp root (mirrors
// erun-ui/environment_activity_observed_test.go's seedMCPForward).
//
// The cache dir and Windows' AppData roots are redirected too, because the
// forward removal also clears the legacy locations they name; left alone, a
// test could remove a record from the operator's real tree.
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
