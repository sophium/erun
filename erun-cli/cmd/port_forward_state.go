package cmd

import (
	"os"
	"path/filepath"
	"strings"

	common "github.com/sophium/erun/erun-common"
)

// The canonical location is shared (erun-common) because the desktop reads the
// same files to tell a reachable environment from one nobody opened. Only the
// migration off the old cache-dir location stays here, with the writer.
func portForwardStatePath(kind, tenant, environment string) (string, error) {
	newPath, err := common.PortForwardStatePath(kind, tenant, environment)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(newPath); err == nil {
		return newPath, nil
	}
	migrateLegacyPortForwardState(kind, tenant, environment, newPath)
	return newPath, nil
}

func migrateLegacyPortForwardState(kind, tenant, environment, newPath string) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return
	}
	legacyPath := filepath.Join(cacheDir, "erun", kind, tenant, environment+".json")
	if _, err := os.Stat(legacyPath); err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return
	}
	if err := os.Rename(legacyPath, newPath); err != nil {
		return
	}
	legacyLog := portForwardLogPath(legacyPath)
	newLog := portForwardLogPath(newPath)
	_ = os.Rename(legacyLog, newLog)
}

func portForwardLogPath(statePath string) string {
	return strings.TrimSuffix(statePath, filepath.Ext(statePath)) + ".log"
}
