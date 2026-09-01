package eruncommon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
)

// seedPortForwardStateFileForTest writes a forward's on-disk state file
// directly, mirroring the shape `erun open` writes, without touching the env
// config -- the caller decides separately whether to seed a config the
// tenant/environment resolves against.
func seedPortForwardStateFileForTest(t *testing.T, tenant, environment string, port int) {
	t.Helper()
	path, err := PortForwardStatePath("mcp", tenant, environment)
	if err != nil {
		t.Fatalf("PortForwardStatePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data, err := json.Marshal(PortForwardState{Tenant: tenant, Environment: environment, LocalPort: port})
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
func redirectConfigHomeForTest(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("HOME", root)
	xdg.Reload()
	t.Cleanup(xdg.Reload)
}

// TestLoadPortForwardStateDeletedEnvironmentReadsAsNoForward is the existing,
// intended behaviour environmentIsConfigured exists for: a state file left
// behind by an environment that was since deleted must not read as a live
// forward, even though the file itself is intact.
func TestLoadPortForwardStateDeletedEnvironmentReadsAsNoForward(t *testing.T) {
	redirectConfigHomeForTest(t)
	tenant, environment := "acme", "dev"
	seedPortForwardStateFileForTest(t, tenant, environment, 12345)
	// Deliberately never SaveEnvConfig: the tenant/environment this file
	// names was never configured (or was deleted), the ordinary case
	// LoadEnvConfig reports as ErrNotInitialized.

	_, established, err := LoadPortForwardState("mcp", tenant, environment)
	if err != nil {
		t.Fatalf("expected no error for a genuinely unconfigured environment, got: %v", err)
	}
	if established {
		t.Fatal("expected a deleted environment's leftover state file to read as no forward")
	}
}

// TestLoadPortForwardStatePropagatesAConfigReadFailureRatherThanReportingNoForward
// is the regression: environmentIsConfigured used to collapse every
// LoadEnvConfig failure -- not just a genuine absence -- into "not
// configured" (false, nil error). A state file whose env config is corrupt
// (as opposed to simply missing) must surface that failure so the caller can
// tell "this environment was deleted" apart from "the config could not be
// read", rather than silently reporting a live forward as gone.
func TestLoadPortForwardStatePropagatesAConfigReadFailureRatherThanReportingNoForward(t *testing.T) {
	redirectConfigHomeForTest(t)
	tenant, environment := "acme", "dev"
	seedPortForwardStateFileForTest(t, tenant, environment, 12345)

	envConfigPath := filepath.Join(xdg.ConfigHome, configRoot, tenant, environment, configFile)
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
	if errors.Is(err, ErrNotInitialized) {
		t.Fatalf("a corrupt config is not a genuine absence, got: %v", err)
	}
	if established {
		t.Fatal("established must be false alongside a real error")
	}
}
