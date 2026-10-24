package eruncommon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
	"github.com/sophium/erun/erun-common/infra/kube"
)

// seedPortForwardStateFileForTest writes a forward's on-disk state file
// directly, mirroring the shape `erun open` writes, without touching the env
// config -- the caller decides separately whether to seed a config the
// tenant/environment resolves against.
func seedPortForwardStateFileForTest(t *testing.T, tenant, environment string, port int) {
	t.Helper()
	path, err := kube.PortForwardStatePath("mcp", tenant, environment)
	if err != nil {
		t.Fatalf("PortForwardStatePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data, err := json.Marshal(kube.PortForwardState{Tenant: tenant, Environment: environment, LocalPort: port})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

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
