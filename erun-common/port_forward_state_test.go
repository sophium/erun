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

// TestPortForwardStatePathUsesTheCanonicalERunSpelling pins the one spelling
// every writer of the per-install state tree shares. A second spelling is not a
// cosmetic difference: on a case-sensitive volume it resolves to a sibling
// directory, so state written under one spelling reads as absent under the
// other, and a case-insensitive volume hides that. The expected path is built
// from the literal rather than from ERunStateDirName, so changing the constant
// fails this test instead of moving the goalpost with it.
func TestPortForwardStatePathUsesTheCanonicalERunSpelling(t *testing.T) {
	redirectConfigHomeForTest(t)

	path, err := PortForwardStatePath("mcp", "acme", "dev")
	if err != nil {
		t.Fatalf("PortForwardStatePath: %v", err)
	}
	want := filepath.Join(xdg.ConfigHome, "ERun", "portforward", "mcp", "acme", "dev.json")
	if path != want {
		t.Fatalf("port-forward state must live at the canonical spelling %q, got %q", want, path)
	}
}

// TestLoadPortForwardStateReadsStateWrittenUnderTheLegacyLowercaseSpelling is
// the counterpart that keeps state already on disk visible: a record a former
// writer left under the lowercase spelling must still read as the live forward
// it is, and must be carried forward to the canonical spelling rather than
// stranded as a second tree.
func TestLoadPortForwardStateReadsStateWrittenUnderTheLegacyLowercaseSpelling(t *testing.T) {
	redirectConfigHomeForTest(t)
	tenant, environment := "acme", "dev"
	if err := (ConfigStore{}).SaveEnvConfig(tenant, EnvConfig{Name: environment}); err != nil {
		t.Fatalf("SaveEnvConfig: %v", err)
	}
	legacyPath := seedLegacyPortForwardStateFileForTest(t, tenant, environment, 12345)

	state, established, err := LoadPortForwardState("mcp", tenant, environment)
	if err != nil {
		t.Fatalf("a record under the legacy spelling must not surface an error, got: %v", err)
	}
	if !established {
		t.Fatal("expected a record written under the legacy lowercase spelling to read as a live forward")
	}
	if state.LocalPort != 12345 {
		t.Fatalf("expected the legacy record's own port, got %d", state.LocalPort)
	}

	canonicalPath, err := PortForwardStatePath("mcp", tenant, environment)
	if err != nil {
		t.Fatalf("PortForwardStatePath: %v", err)
	}
	if _, err := os.Stat(canonicalPath); err != nil {
		t.Fatalf("expected the legacy record to be carried forward to %q, got stat err=%v", canonicalPath, err)
	}
	if !resolvesToSameFileForTest(legacyPath, canonicalPath) {
		if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
			t.Fatalf("expected the legacy record to be moved forward, not copied, got stat err=%v", err)
		}
	}
}

// seedLegacyPortForwardStateFileForTest writes a forward's record under the
// legacy lowercase spelling of the state directory, the location a former
// writer left it at.
func seedLegacyPortForwardStateFileForTest(t *testing.T, tenant, environment string, port int) string {
	t.Helper()
	path, err := LegacyPortForwardStatePath("mcp", tenant, environment)
	if err != nil {
		t.Fatalf("LegacyPortForwardStatePath: %v", err)
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
	return path
}

// resolvesToSameFileForTest reports whether two paths are the same file, which
// on a case-insensitive volume is how the legacy and canonical spellings of the
// state directory resolve to one directory. It reports false when either path is
// absent, so the caller can tell "moved" from "one file under two spellings".
func resolvesToSameFileForTest(a, b string) bool {
	aInfo, err := os.Stat(a)
	if err != nil {
		return false
	}
	bInfo, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(aInfo, bInfo)
}
