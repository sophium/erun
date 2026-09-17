package eruncommon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
)

// setConfigHomeForModeTest points config resolution at a fresh temp dir and
// returns a restore func.
func setConfigHomeForModeTest(t *testing.T) func() {
	t.Helper()
	previous := xdg.ConfigHome
	xdg.ConfigHome = filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(xdg.ConfigHome, 0o700); err != nil {
		t.Fatalf("mkdir config home: %v", err)
	}
	return func() { xdg.ConfigHome = previous }
}

func assertConfigFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s: mode = %04o, want %04o", path, got, want)
	}
}

// rewriteAsLegacyModeAndResave proves that a config already on disk at the old
// 0644 is corrected by the next write rather than left as it is.
func rewriteAsLegacyModeAndResave(t *testing.T, path string, save func()) {
	t.Helper()
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	save()
	assertConfigFileMode(t, path, 0o600)
}

func assertBackupsAreRestricted(t *testing.T, backups []ConfigBackup) {
	t.Helper()
	if len(backups) == 0 {
		t.Fatal("expected a dated backup to have been written")
	}
	for _, backup := range backups {
		assertConfigFileMode(t, backup.Path, 0o600)
	}
}

// TestRootConfigAndItsBackupsAreWrittenRestricted pins that the root
// config.yaml -- which holds cluster admin tokens -- and its dated backups are
// created 0600. The containing directory being 0700 is what protects the file
// today; the file's own mode is what travels with it into backups, tarballs,
// support bundles and synced folders.
func TestRootConfigAndItsBackupsAreWrittenRestricted(t *testing.T) {
	restore := setConfigHomeForModeTest(t)
	defer restore()

	save := func() {
		if err := SaveERunConfig(ERunConfig{DefaultTenant: "erun"}); err != nil {
			t.Fatalf("save root config: %v", err)
		}
	}
	save()
	_, rootPath, err := LoadERunConfig()
	if err != nil {
		t.Fatalf("load root config: %v", err)
	}
	assertConfigFileMode(t, rootPath, 0o600)

	rewriteAsLegacyModeAndResave(t, rootPath, save)

	backups, err := ListRootConfigBackups(rootPath)
	if err != nil {
		t.Fatalf("list root backups: %v", err)
	}
	assertBackupsAreRestricted(t, backups)
}

// TestEnvConfigAndItsBackupsAreWrittenRestricted covers the per-environment
// config.yaml and its dated backups under the same contract as the root config.
func TestEnvConfigAndItsBackupsAreWrittenRestricted(t *testing.T) {
	restore := setConfigHomeForModeTest(t)
	defer restore()

	save := func() {
		if err := SaveEnvConfig("erun", EnvConfig{Name: "dev"}); err != nil {
			t.Fatalf("save env config: %v", err)
		}
	}
	save()
	envPath, err := EnvConfigPath("erun", "dev")
	if err != nil {
		t.Fatalf("env config path: %v", err)
	}
	assertConfigFileMode(t, envPath, 0o600)

	rewriteAsLegacyModeAndResave(t, envPath, save)

	backups, err := ListEnvConfigBackups("erun", "dev")
	if err != nil {
		t.Fatalf("list env backups: %v", err)
	}
	assertBackupsAreRestricted(t, backups)
}
