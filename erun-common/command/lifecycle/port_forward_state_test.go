package lifecycle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
	"github.com/sophium/erun/erun-common/infra/kube"
)

// seedPortForwardStateFileForTest writes a forward's state file without touching env config.
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

// adrg/xdg caches ConfigHome at init, so xdg.Reload is needed after Setenv. Cache and AppData
// roots are redirected too since removal also clears legacy locations.
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
