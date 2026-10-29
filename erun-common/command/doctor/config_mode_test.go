package doctor

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
)

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

// --sync-config writes config through its own path, so it must keep the restricted mode.
func TestDoctorSyncConfigWritesRestrictedConfig(t *testing.T) {
	requirePOSIXFileModes(t)
	configHome := t.TempDir()
	env := syncConfigTestEnv(map[string]string{"ERUN_CLOUD_CONTEXT_NAME": ""})

	inspection, err := InspectRuntimeConfigSync(configHome, env)
	mustNoErr(t, err, "inspect")
	mustNoErr(t, RunRuntimeConfigSync(testTraceContext(false), inspection), "sync")

	rootPath := filepath.Join(configHome, config.ConfigRoot, config.ConfigFile)
	envPath := filepath.Join(configHome, config.ConfigRoot, "team", "prod", config.ConfigFile)
	for _, path := range []string{rootPath, envPath} {
		assertConfigFileMode(t, path, 0o600)
	}
}
