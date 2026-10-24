package kube

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// PortForwardStatePath is a forward's state location. It lives under the config dir, not the
// cache, so eviction cannot orphan a running forward, and resolves through configRoot
// (xdg.ConfigHome, not os.UserConfigDir) so it sits beside the config it is validated against.
func PortForwardStatePath(kind, tenant, environment string) (string, error) {
	kind = strings.TrimSpace(kind)
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if kind == "" || tenant == "" || environment == "" {
		return "", fmt.Errorf("kind, tenant and environment are required")
	}
	for _, segment := range []struct{ kind, value string }{
		{"port-forward kind", kind},
		{"tenant", tenant},
		{"environment", environment},
	} {
		if err := config.ValidateStatePathSegment(segment.kind, segment.value); err != nil {
			return "", err
		}
	}
	return config.ResolveConfigFilePath(filepath.Join(config.ConfigRoot, "portforward", kind, tenant, environment+".json"))
}

// ResolvePortForwardStatePath first migrates a legacy-location record and its log into the
// canonical tree. A dry run moves nothing and returns the legacy path it found.
func ResolvePortForwardStatePath(kind, tenant, environment string, dryRun bool) (string, error) {
	statePath, err := PortForwardStatePath(kind, tenant, environment)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(statePath); err == nil {
		return statePath, nil
	}
	for _, legacyPath := range legacyPortForwardStatePaths(kind, tenant, environment, statePath) {
		if _, err := os.Stat(legacyPath); err != nil {
			continue
		}
		if dryRun {
			return legacyPath, nil
		}
		migrateLegacyPortForwardState(legacyPath, statePath)
		return statePath, nil
	}
	return statePath, nil
}

// legacyPortForwardStatePaths lists earlier record locations, oldest first, excluding the canonical one.
func legacyPortForwardStatePaths(kind, tenant, environment, statePath string) []string {
	var paths []string
	if cacheDir, err := os.UserCacheDir(); err == nil {
		paths = append(paths, filepath.Join(cacheDir, config.ConfigRoot, kind, tenant, environment+".json"))
	}
	if root, ok := legacyPortForwardRoot(); ok {
		paths = append(paths, filepath.Join(root, kind, tenant, environment+".json"))
	}
	legacy := paths[:0]
	for _, path := range paths {
		if filepath.Clean(path) != filepath.Clean(statePath) {
			legacy = append(legacy, path)
		}
	}
	return legacy
}

// legacyPortForwardRoot is the os.UserConfigDir forward tree, reported only where it differs
// (macOS with XDG_CONFIG_HOME set, Windows).
func legacyPortForwardRoot() (string, bool) {
	configDir, err := legacyUserConfigDir()
	if err != nil {
		return "", false
	}
	legacy := filepath.Join(configDir, config.ConfigRoot, "portforward")
	current, err := portForwardRoot()
	if err == nil && filepath.Clean(legacy) == filepath.Clean(current) {
		return "", false
	}
	return legacy, true
}

// legacyUserConfigDir keys the macOS branch on the detected host so tests can pin it anywhere.
func legacyUserConfigDir() (string, error) {
	if core.DetectHost().OS == model.HostOSDarwin {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support"), nil
	}
	return os.UserConfigDir()
}

// migrateLegacyPortForwardState is best-effort: an unmovable record stays put.
func migrateLegacyPortForwardState(legacyPath, statePath string) {
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		return
	}
	if err := os.Rename(legacyPath, statePath); err != nil {
		return
	}
	legacyLog := PortForwardLogPathForState(legacyPath)
	newLog := PortForwardLogPathForState(statePath)
	_ = os.Rename(legacyLog, newLog)
	_ = os.Rename(legacyLog+".1", newLog+".1")
}

// PortForwardLogPathForState derives the log path the writer records in logPath; readers and
// writers must share it.
func PortForwardLogPathForState(statePath string) string {
	return strings.TrimSuffix(statePath, filepath.Ext(statePath)) + ".log"
}

// PortForwardLogPath is PortForwardLogPathForState resolved from a record's identity.
func PortForwardLogPath(kind, tenant, environment string) (string, error) {
	statePath, err := PortForwardStatePath(kind, tenant, environment)
	if err != nil {
		return "", err
	}
	return PortForwardLogPathForState(statePath), nil
}

// RemovePortForwardRecord removes a forward's state file, log, and rotated log, at canonical and
// legacy locations. A log a live forward still writes to is kept; the state file always goes,
// since its port is reissued to the next environment.
func RemovePortForwardRecord(ctx core.Context, kind, tenant, environment string) error {
	statePath, err := PortForwardStatePath(kind, tenant, environment)
	if err != nil {
		return err
	}
	if err := removePortForwardRecordAt(ctx, kind, statePath); err != nil {
		return err
	}
	for _, legacyPath := range legacyPortForwardStatePaths(kind, tenant, environment, statePath) {
		if err := removePortForwardRecordAt(ctx, kind, legacyPath); err != nil {
			return err
		}
	}
	return nil
}

func removePortForwardRecordAt(ctx core.Context, kind, statePath string) error {
	// Never trust the record's logPath: it could point outside the forward tree.
	logPath := PortForwardLogPathForState(statePath)
	// The record's process covers the normal case; the open-file check covers a log whose state
	// file is already gone. Check ".1" too, since rotation renames the file the writer holds.
	if record, ok := readPortForwardStateFile(statePath); ok && core.IsProcessAlive(record.ProcessID) {
		ctx.Trace(fmt.Sprintf("%s: keeping port-forward log %s: the forward that recorded it (PID %d) is still running", kind, logPath, record.ProcessID))
	} else if portForwardLogHeldByProcess(logPath) {
		ctx.Trace(fmt.Sprintf("%s: keeping port-forward log %s: a process still holds it open", kind, logPath))
	} else {
		if err := removeFileIfPresent(ctx, logPath); err != nil {
			return err
		}
		if err := removeFileIfPresent(ctx, logPath+".1"); err != nil {
			return err
		}
	}
	return removeFileIfPresent(ctx, statePath)
}

// portForwardLogHeldByProcess is a test seam so tests need not depend on lsof.
var portForwardLogHeldByProcess = logHeldByLiveProcess

// logHeldByLiveProcess reports false when it cannot ask (no lsof, Windows, probe failure).
func logHeldByLiveProcess(logPath string) bool {
	if core.DetectHost().OS == model.HostOSWindows {
		return false
	}
	for _, path := range []string{logPath, logPath + ".1"} {
		out, err := core.Command("lsof", "-nP", "--", path).Output()
		if err != nil {
			continue
		}
		// A non-zero lsof exit means no holder; skip the header line.
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "COMMAND") {
				return true
			}
		}
	}
	return false
}

// readPortForwardStateFile bypasses LoadPortForwardState's configured-environment filter, since
// the record's processId is the only evidence its log is still written.
func readPortForwardStateFile(path string) (model.PortForwardState, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return model.PortForwardState{}, false
	}
	var state model.PortForwardState
	if err := json.Unmarshal(data, &state); err != nil {
		return model.PortForwardState{}, false
	}
	return state, true
}

// removeFileIfPresent skips files it cannot stat so teardown never fails on them.
func removeFileIfPresent(ctx core.Context, path string) error {
	if !pathStats(path) {
		return nil
	}
	ctx.TraceCommand("", "rm", "-f", path)
	if ctx.DryRun {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func pathStats(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// LoadPortForwardState reads a forward's state. A missing file, or one naming an unconfigured
// environment, reads as no forward: the orphan's port may now belong to another environment.
func LoadPortForwardState(kind, tenant, environment string) (model.PortForwardState, bool, error) {
	path, err := PortForwardStatePath(kind, tenant, environment)
	if err != nil {
		return model.PortForwardState{}, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return model.PortForwardState{}, false, nil
		}
		return model.PortForwardState{}, false, err
	}
	var state model.PortForwardState
	if err := json.Unmarshal(data, &state); err != nil {
		return model.PortForwardState{}, false, fmt.Errorf("%s: %w", path, err)
	}
	configured, err := environmentIsConfigured(tenant, environment)
	if err != nil {
		return model.PortForwardState{}, false, err
	}
	if !configured {
		return model.PortForwardState{}, false, nil
	}
	return state, state.LocalPort > 0, nil
}

// environmentIsConfigured returns false only for ErrNotInitialized; other read failures are returned.
func environmentIsConfigured(tenant, environment string) (bool, error) {
	_, _, err := config.ConfigStore{}.LoadEnvConfig(tenant, environment)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, config.ErrNotInitialized) {
		return false, nil
	}
	return false, err
}
