package cmd

import (
	"os"
	"path/filepath"
	"strings"
)

// portForwardStatePath returns the on-disk path for a port-forward state
// file, scoped by kind (mcp / sshd / api), tenant, and environment.
//
// State now lives under os.UserConfigDir() rather than os.UserCacheDir().
// Cache directories are explicitly evictable by the OS and by user-level
// cleanup tools, and we saw the eviction-then-orphan failure mode in
// practice: macOS purges ~/Library/Caches/erun/..., the detached `kubectl
// port-forward` outlives the record, and the next `erun open` has no way
// to recognise its own forward.
//
// For installs that already have state under the legacy cache path,
// portForwardStatePath performs a one-time, silent rename on first access
// so the relink does not require user action.
func portForwardStatePath(kind, tenant, environment string) (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	newPath := filepath.Join(configDir, "erun", "portforward", kind, tenant, environment+".json")

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
