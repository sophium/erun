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

// portForwardLogMaxBytes bounds each kubectl port-forward log. A log that
// outgrows it is rolled to its ".1" generation before the next forward appends,
// so the busiest environment cannot fill the disk, while a log under the cap is
// left untouched and stays readable.
const portForwardLogMaxBytes = 5 * 1024 * 1024

// openPortForwardLog opens the log a kubectl port-forward writes to, rotating
// an over-cap log first. All three forwards share it so the bounding rule
// cannot drift between them.
func openPortForwardLog(logPath string) (*os.File, error) {
	return common.OpenBoundedAppendLog(logPath, portForwardLogMaxBytes, 0o644)
}

// rotatePortForwardLogIfOversized re-applies that cap to a forward that is
// already running. openPortForwardLog bounds the log only when a fresh one is
// opened, and a healthy forward is deliberately reused rather than restarted
// -- it holds the file it opened at start as its own stdout/stderr -- so a
// forward that stays up for weeks never reaches that rotation again and grows
// its log without bound. Called from the paths that find or adopt a live
// forward, so every touch of one re-applies the same cap, and it also reclaims
// a log that had already grown past it before this existed.
//
// Best-effort and silent on failure: rotation is diagnostics housekeeping and
// must never stop a healthy forward from being reused.
func rotatePortForwardLogIfOversized(ctx common.Context, kind, logPath string) {
	if strings.TrimSpace(logPath) == "" {
		return
	}
	if common.RotateOversizedLog(logPath, portForwardLogMaxBytes) {
		ctx.Trace(fmt.Sprintf("%s: rotated oversized port-forward log %s (kept a %s.1 backup)", kind, logPath, logPath))
	}
}
