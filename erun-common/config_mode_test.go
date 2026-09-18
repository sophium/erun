package eruncommon

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/adrg/xdg"
)

// The config.yaml files hold cluster admin tokens. Their containing directory
// is 0700 today, so the file mode is what still protects them once a copy
// leaves that directory; these tests pin the mode of the files themselves.

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

func requirePOSIXFileModes(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced the same way on windows")
	}
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

// TestRootConfigAndItsBackupsAreWrittenRestricted pins the mode of the root
// config.yaml and of a generated dated backup, and that an existing file at the
// old 0644 mode is corrected by the next write.
func TestRootConfigAndItsBackupsAreWrittenRestricted(t *testing.T) {
	requirePOSIXFileModes(t)
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
	requirePOSIXFileModes(t)
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

// TestDoctorSyncConfigWritesRestrictedConfig covers the other writer of these
// files: `erun doctor --sync-config` rewrites the root and environment config
// through its own path, so a reconcile must not restore the wide mode.
func TestDoctorSyncConfigWritesRestrictedConfig(t *testing.T) {
	requirePOSIXFileModes(t)
	configHome := t.TempDir()
	env := syncConfigTestEnv(map[string]string{"ERUN_CLOUD_CONTEXT_NAME": ""})

	inspection, err := InspectRuntimeConfigSync(configHome, env)
	mustNoErr(t, err, "inspect")
	mustNoErr(t, RunRuntimeConfigSync(testTraceContext(false), inspection), "sync")

	rootPath := filepath.Join(configHome, configRoot, configFile)
	envPath := filepath.Join(configHome, configRoot, "team", "prod", configFile)
	for _, path := range []string{rootPath, envPath} {
		assertConfigFileMode(t, path, 0o600)
	}
}
