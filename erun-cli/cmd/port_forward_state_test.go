package cmd

import (
	"os"
	"path/filepath"
	"testing"

	common "github.com/sophium/erun/erun-common"
)

func seedLegacyPortForwardState(t *testing.T, cacheHome, kind, tenant, environment string) string {
	t.Helper()
	legacyPath := filepath.Join(cacheHome, "erun", kind, tenant, environment+".json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("seed legacy dir: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte(`{"localPort":1}`), 0o644); err != nil {
		t.Fatalf("seed legacy file: %v", err)
	}
	return legacyPath
}

// TestPortForwardStatePathDryRunDoesNotMigrateLegacyState pins the same
// dry-run purity contract erun#1907 fixed for the config tree: resolving a
// port-forward's state path is a read every ensure*PortForward call makes
// before its own dry-run check, so a dry run must not move the operator's
// legacy-cache-dir state file to its new location.
func TestPortForwardStatePathDryRunDoesNotMigrateLegacyState(t *testing.T) {
	configHome := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	legacyPath := seedLegacyPortForwardState(t, cacheHome, "api", "acme", "dev")

	path, err := portForwardStatePath("api", "acme", "dev", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != legacyPath {
		t.Fatalf("expected dry-run to read the still-legacy path %q, got %q", legacyPath, path)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("dry-run must not move the legacy file, got stat err=%v", err)
	}
	newPath, err := common.PortForwardStatePath("api", "acme", "dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not create the new-location file, got stat err=%v", err)
	}
}

// TestPortForwardStatePathMigratesLegacyStateWhenNotDryRun locks in that a
// real (non-dry-run) resolution still performs the one-time migration the
// dry-run case above must skip.
func TestPortForwardStatePathMigratesLegacyStateWhenNotDryRun(t *testing.T) {
	configHome := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	legacyPath := seedLegacyPortForwardState(t, cacheHome, "api", "acme", "dev")

	path, err := portForwardStatePath("api", "acme", "dev", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	newPath, err := common.PortForwardStatePath("api", "acme", "dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != newPath {
		t.Fatalf("expected the migrated path %q, got %q", newPath, path)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("expected the legacy file to be moved away, got stat err=%v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("expected the migrated file at the new path, got stat err=%v", err)
	}
}
