package cmd

import (
	"os"
	"path/filepath"
	"strings"
)

// State lives under the config dir, not the cache dir: an evicted cache
// entry would orphan the still-running kubectl port-forward and leave the
// next `erun open` unable to recognise its own forward.
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
