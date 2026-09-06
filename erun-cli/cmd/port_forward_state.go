package cmd

import (
	"fmt"
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

// rotatePortForwardLogIfOversized bounds a forward's own kubectl log so a
// forward that stays healthy and reused for weeks cannot grow it without
// limit, and reclaims a log that already grew unbounded before this existed
// (erun#2161). Called every time this env's forward is found alive --
// serving, adopted from a foreign process, or just started -- so a long-lived
// forward gets checked on every touch rather than only at kubectl's own
// startup.
//
// Best-effort and silent on failure: rotation is diagnostics-adjacent
// housekeeping and must never block a healthy forward from being reused.
func rotatePortForwardLogIfOversized(ctx common.Context, kind, logPath string) {
	if strings.TrimSpace(logPath) == "" {
		return
	}
	rotated, err := common.RotateOversizedFile(logPath, common.PortForwardLogMaxBytes)
	if err != nil {
		ctx.Trace(fmt.Sprintf("%s: could not rotate oversized port-forward log %s: %s", kind, logPath, err.Error()))
		return
	}
	if rotated {
		ctx.Trace(fmt.Sprintf("%s: rotated oversized port-forward log %s (kept a %s.1 backup)", kind, logPath, logPath))
	}
}
