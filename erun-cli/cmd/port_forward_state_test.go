package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	common "github.com/sophium/erun/erun-common"
)

// isolatePortForwardStateDirs points every root the state path resolution reads
// at one fresh temp tree, then proves the redirect took before the test relies
// on it.
//
// The XDG_* variables alone are only half of it: os.UserConfigDir and
// os.UserCacheDir honour them on the platforms that follow the XDG spec, but on
// darwin they resolve under $HOME/Library instead. A test that set only the XDG
// variables left the code under test reading the operator's real home while its
// own seed file sat in the temp dir, so it failed on a path mismatch several
// assertions later rather than on the platform assumption that caused it. HOME
// is what moves both roots everywhere; APPDATA/LOCALAPPDATA do it on windows.
//
// The check below turns whatever gap remains between "the directories I set"
// and "the directories the code reads" into a failure naming that cause, on the
// platform where it is true, instead of a confusing comparison three steps on.
func isolatePortForwardStateDirs(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("APPDATA", filepath.Join(home, "appdata"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "localappdata"))

	for _, resolved := range []struct {
		name string
		fn   func() (string, error)
	}{
		{"os.UserConfigDir", os.UserConfigDir},
		{"os.UserCacheDir", os.UserCacheDir},
	} {
		dir, err := resolved.fn()
		if err != nil {
			t.Fatalf("%s: %v", resolved.name, err)
		}
		rel, err := filepath.Rel(home, dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Fatalf("%s resolved to %q, outside the isolated home %q, so this test would read the operator's real state", resolved.name, dir, home)
		}
	}
	return home
}

// seedLegacyPortForwardState writes a state file at the legacy cache-dir
// location, resolved the way the code under test resolves it. A hand-built path
// under a second temp dir only happens to agree with that lookup on the
// platforms that honour XDG_*, which is what made the two tests below
// darwin-only failures.
func seedLegacyPortForwardState(t *testing.T, kind, tenant, environment string) string {
	t.Helper()
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("resolve cache dir: %v", err)
	}
	legacyPath := filepath.Join(cacheDir, "erun", kind, tenant, environment+".json")
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
	isolatePortForwardStateDirs(t)

	legacyPath := seedLegacyPortForwardState(t, "api", "acme", "dev")

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
	isolatePortForwardStateDirs(t)

	legacyPath := seedLegacyPortForwardState(t, "api", "acme", "dev")

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
