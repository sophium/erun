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

// TestPortForwardStatePathCarriesTheLowercaseSpelledStateForward pins the
// migration off the lowercase spelling of the state directory. On a
// case-sensitive volume that spelling is a sibling directory rather than the
// same one, so a record under it has to be carried to the canonical spelling
// instead of reported as a missing forward.
func TestPortForwardStatePathCarriesTheLowercaseSpelledStateForward(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	caseInsensitive := caseInsensitiveVolumeForTest(t, configHome)

	legacyPath := seedLegacyLowercaseSpelledPortForwardState(t, configHome, "api", "acme", "dev")

	path, err := portForwardStatePath("api", "acme", "dev", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	newPath, err := common.PortForwardStatePath("api", "acme", "dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != newPath {
		t.Fatalf("expected the carried-forward path %q, got %q", newPath, path)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("expected the record at the canonical path %q, got stat err=%v", newPath, err)
	}
	if !caseInsensitive {
		if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
			t.Fatalf("expected the legacy-spelled record to be moved away, got stat err=%v", err)
		}
	}
}

// TestPortForwardStatePathDryRunKeepsTheLowercaseSpelledStateInPlace pins the
// dry-run purity contract for this migration too: resolving the path is a read
// every ensure*PortForward call makes before its own dry-run check, so a dry run
// must report where the record lives today without moving it.
func TestPortForwardStatePathDryRunKeepsTheLowercaseSpelledStateInPlace(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	caseInsensitive := caseInsensitiveVolumeForTest(t, configHome)

	legacyPath := seedLegacyLowercaseSpelledPortForwardState(t, configHome, "api", "acme", "dev")
	newPath, err := common.PortForwardStatePath("api", "acme", "dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	path, err := portForwardStatePath("api", "acme", "dev", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !caseInsensitive && path != legacyPath {
		t.Fatalf("expected a dry run to report the still-legacy path %q, got %q", legacyPath, path)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("dry-run must not move the legacy record, got stat err=%v", err)
	}
	if !caseInsensitive {
		if _, err := os.Stat(newPath); !os.IsNotExist(err) {
			t.Fatalf("dry-run must not create the canonical record, got stat err=%v", err)
		}
	}
}

func seedLegacyLowercaseSpelledPortForwardState(t *testing.T, configHome, kind, tenant, environment string) string {
	t.Helper()
	legacyPath := filepath.Join(configHome, "erun", "portforward", kind, tenant, environment+".json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("seed lowercase-spelled dir: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte(`{"localPort":1}`), 0o644); err != nil {
		t.Fatalf("seed lowercase-spelled file: %v", err)
	}
	return legacyPath
}

// caseInsensitiveVolumeForTest reports whether dir sits on a case-insensitive
// volume, where both spellings of the state directory resolve to one directory
// and the move assertions above are vacuous rather than false.
func caseInsensitiveVolumeForTest(t *testing.T, dir string) bool {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("probe dir: %v", err)
	}
	probe := filepath.Join(dir, "CaseSensitivityProbe")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		t.Fatalf("probe write: %v", err)
	}
	defer func() { _ = os.Remove(probe) }()
	_, err := os.Stat(filepath.Join(dir, "casesensitivityprobe"))
	return err == nil
}
