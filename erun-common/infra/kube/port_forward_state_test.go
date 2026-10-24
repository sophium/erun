package kube

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

// seedPortForwardStateFileForTest writes a state file without touching the env config.
func seedPortForwardStateFileForTest(t *testing.T, tenant, environment string, port int) {
	t.Helper()
	path, err := PortForwardStatePath("mcp", tenant, environment)
	if err != nil {
		t.Fatalf("PortForwardStatePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data, err := json.Marshal(model.PortForwardState{Tenant: tenant, Environment: environment, LocalPort: port})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// redirectConfigHomeForTest points the config store, cache dir, and AppData at a temp root.
// xdg caches ConfigHome at init, so xdg.Reload is required.
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

func TestLoadPortForwardStateDeletedEnvironmentReadsAsNoForward(t *testing.T) {
	redirectConfigHomeForTest(t)
	tenant, environment := "acme", "dev"
	seedPortForwardStateFileForTest(t, tenant, environment, 12345)

	_, established, err := LoadPortForwardState("mcp", tenant, environment)
	if err != nil {
		t.Fatalf("expected no error for a genuinely unconfigured environment, got: %v", err)
	}
	if established {
		t.Fatal("expected a deleted environment's leftover state file to read as no forward")
	}
}

func TestLoadPortForwardStatePropagatesAConfigReadFailureRatherThanReportingNoForward(t *testing.T) {
	redirectConfigHomeForTest(t)
	tenant, environment := "acme", "dev"
	seedPortForwardStateFileForTest(t, tenant, environment, 12345)

	envConfigPath := filepath.Join(xdg.ConfigHome, config.ConfigRoot, tenant, environment, config.ConfigFile)
	if err := os.MkdirAll(filepath.Dir(envConfigPath), 0o755); err != nil {
		t.Fatalf("MkdirAll env config dir: %v", err)
	}
	if err := os.WriteFile(envConfigPath, []byte("not: [valid yaml"), 0o644); err != nil {
		t.Fatalf("seed corrupt env config: %v", err)
	}

	_, established, err := LoadPortForwardState("mcp", tenant, environment)
	if err == nil {
		t.Fatal("expected the corrupt env config to surface as an error, not silently report no forward")
	}
	if errors.Is(err, config.ErrNotInitialized) {
		t.Fatalf("a corrupt config is not a genuine absence, got: %v", err)
	}
	if established {
		t.Fatal("established must be false alongside a real error")
	}
}
