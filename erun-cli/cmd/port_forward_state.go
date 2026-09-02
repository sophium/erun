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
//
// dryRun must skip the migration itself — a dry run resolving this path must
// not move the operator's own file on disk — but still needs to report where
// the state actually lives today, so an unmigrated legacy path is returned
// as-is rather than the not-yet-existing new one.
func portForwardStatePath(kind, tenant, environment string, dryRun bool) (string, error) {
	newPath, err := common.PortForwardStatePath(kind, tenant, environment)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(newPath); err == nil {
		return newPath, nil
	}
	legacyPath, err := legacyPortForwardStatePath(kind, tenant, environment)
	if err != nil {
		return newPath, nil
	}
	if _, err := os.Stat(legacyPath); err != nil {
		return newPath, nil
	}
	if dryRun {
		return legacyPath, nil
	}
	migrateLegacyPortForwardState(legacyPath, newPath)
	return newPath, nil
}

func legacyPortForwardStatePath(kind, tenant, environment string) (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, "erun", kind, tenant, environment+".json"), nil
}

func migrateLegacyPortForwardState(legacyPath, newPath string) {
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
