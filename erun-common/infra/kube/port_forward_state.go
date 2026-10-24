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
)

// A port-forward state file is the record that something established a local
// forward to an environment. `erun open` writes one whoever ran it — the
// operator at a terminal or the desktop shelling out — which is what makes it
// the honest answer to "is this environment reachable", as opposed to "did the
// desktop open it". Reading it belongs here rather than in the CLI because both
// transports need the same answer from the same path.

// PortForwardState is the persisted record for one forward. It is the shape the
// mcp and api forwards write; the sshd forward adds fields, which unmarshal
// harmlessly into the extras this reader ignores.
type PortForwardState struct {
	Tenant            string `json:"tenant"`
	Environment       string `json:"environment"`
	KubernetesContext string `json:"kubernetesContext"`
	Namespace         string `json:"namespace"`
	LocalPort         int    `json:"localPort"`
	LogPath           string `json:"logPath,omitempty"`
	ProcessID         int    `json:"processId,omitempty"`
}

// PortForwardStatePath is the canonical location for a forward's state. Under
// the config dir, not the cache dir: an evicted cache entry would orphan the
// still-running kubectl port-forward and leave the next `erun open` unable to
// recognise its own forward.
//
// The config tree is named through configRoot rather than spelled out again
// here, because a second spelling is a second thing to keep in step: state
// that resolved to a different root than the tenant config it is validated
// against would stop being found, taking the recorded local port and the log
// beside it out of reach. This tree is the lowercase one; the desktop's own
// per-installation state is a separate capitalized tree, and the two are only
// indistinguishable on a case-insensitive volume. The config home is the one
// the tenant config resolves through (xdg.ConfigHome), not os.UserConfigDir:
// on macOS the latter ignores XDG_CONFIG_HOME and on Windows it names a
// different AppData root, either of which would put this tree beside a
// config tree other than the one it is validated against.
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

// ResolvePortForwardStatePath is PortForwardStatePath for the writer: a record
// an earlier release left at a legacy location is moved into the canonical
// tree first, with its log, so a forward that was running across the upgrade
// is still found instead of being started a second time.
//
// A dry run moves nothing — resolving this path is a read every forward setup
// makes before its own dry-run check — and returns the legacy path it found, so
// it still reports where the record lives today.
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

// legacyPortForwardStatePaths lists where earlier releases kept a forward's
// record, oldest layout first: the cache dir, then the tree os.UserConfigDir
// named. A location that coincides with the canonical path is left out.
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

// legacyPortForwardRoot is the forward tree as it resolved through
// os.UserConfigDir, reported only where it differs from the current one: on
// macOS with XDG_CONFIG_HOME set and on Windows. Elsewhere the two coincide and
// there is no second tree to look in.
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

// legacyUserConfigDir is os.UserConfigDir with the macOS branch keyed on the
// detected host, so the integration suite can pin that layout on any host.
func legacyUserConfigDir() (string, error) {
	if core.DetectHost().OS == core.HostOSDarwin {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support"), nil
	}
	return os.UserConfigDir()
}

// migrateLegacyPortForwardState moves a legacy record, its log and the log's
// rotated generation into the canonical tree. Best-effort: a record that cannot
// be moved is left where it is, and the forward setup carries on with the
// canonical path.
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

// PortForwardLogPathForState is where a forward's kubectl output accumulates:
// the state-file path with its extension replaced, which is exactly how the
// state record's own logPath field is derived when it is written. Both
// directions go through this one spelling, because a reader that derived the
// log differently from the writer would look for a file the writer never
// touched.
//
// It takes the state path rather than the record's identity so the migration
// and removal of a legacy-location record can derive the log beside a path
// that is not the canonical one.
func PortForwardLogPathForState(statePath string) string {
	return strings.TrimSuffix(statePath, filepath.Ext(statePath)) + ".log"
}

// PortForwardLogPath is that same log resolved from a record's identity.
func PortForwardLogPath(kind, tenant, environment string) (string, error) {
	statePath, err := PortForwardStatePath(kind, tenant, environment)
	if err != nil {
		return "", err
	}
	return PortForwardLogPathForState(statePath), nil
}

// RemovePortForwardRecord removes everything one forward leaves on disk: the
// state file, the log its kubectl output was appended to, and the rotated
// generation the log cap leaves beside it. Removing the state file alone frees
// the local port but strands the log next to it, and a log whose environment
// is gone is residue nothing will ever open, restart, or rotate again -- which
// is how the forward tree accumulated hundreds of megabytes that no per-log
// cap could ever reclaim, since a cap bounds a log that is still being written
// and says nothing about the ones nobody will write to again.
//
// One thing it must not remove is a log a live forward is still writing to.
// The forward holds the file it opened at start as its own stdout/stderr, so
// unlinking it would keep consuming disk invisibly while taking away the
// forward the operator is still reading. The record is read before anything is
// removed, and a recorded process that is still alive keeps its log.
//
// The state file goes regardless: the local port it names is already freed and
// reissued to whichever environment is created next, so a record that outlived
// its environment resolves to a live forward belonging to somebody else, which
// is the failure removing it exists to prevent.
//
// A record still at a legacy location is removed by the same rules: it is
// unmigrated only because nothing opened the environment since the upgrade, and
// the next open would otherwise move it into the canonical tree for an
// environment of the same name created later.
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
	// Derived from the state path, never read from the record's own logPath:
	// a hand-edited or half-written record must not be able to point the
	// removal at a file outside the forward tree.
	logPath := PortForwardLogPathForState(statePath)
	// Two ways a log can still have a writer, and both have to be checked.
	// The record's own process answers it whenever there is a record; the
	// open-file check answers the case where there is not — deleting an
	// environment whose forward was still running keeps the log and takes the
	// state file with it, so by the next sweep the record that would have
	// answered is gone. Without the second check the delete path would keep a
	// log the sweep then removed, and the guard would be decorative.
	//
	// The ".1" generation is checked with the live log because rotation
	// renames rather than copies: after a rotation the writer holds the file
	// that is now named ".1", so checking only the live path would unlink the
	// file the forward is actually writing to.
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

// portForwardLogHeldByProcess is the seam a test overrides to decide, without
// depending on lsof being installed, whether the log or the generation beside
// it is still open somewhere.
var portForwardLogHeldByProcess = logHeldByLiveProcess

// logHeldByLiveProcess reports whether any process holds the log, or the
// rotated generation the cap leaves beside it, open. Nothing is reported as
// held when the question cannot be asked — no lsof on the host, a Windows
// host, a probe that failed — which leaves the reclaim behaving as it did
// before this check rather than refusing to run.
func logHeldByLiveProcess(logPath string) bool {
	if core.DetectHost().OS == core.HostOSWindows {
		return false
	}
	for _, path := range []string{logPath, logPath + ".1"} {
		out, err := core.Command("lsof", "-nP", "--", path).Output()
		if err != nil {
			continue
		}
		// lsof exits non-zero when nothing holds the file, so reaching here
		// already means a holder; the header it prints either way is skipped
		// so a version that reports no holder on stdout says so the same way.
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "COMMAND") {
				return true
			}
		}
	}
	return false
}

// readPortForwardStateFile reads the raw record at path, bypassing
// LoadPortForwardState's "is this environment still configured" filter. That
// filter is what makes a deleted environment's record read as "no forward",
// which is right for resolving a forward and wrong here, where the record's
// own processId is the only evidence of whether its log is still being
// written. A missing or unreadable record reads as "no live forward", which is
// also what an environment with no record at all gets.
func readPortForwardStateFile(path string) (PortForwardState, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return PortForwardState{}, false
	}
	var state PortForwardState
	if err := json.Unmarshal(data, &state); err != nil {
		return PortForwardState{}, false
	}
	return state, true
}

// removeFileIfPresent removes one file, tracing it first so a dry run shows the
// same removals it would perform. A stat failure that is not a genuine absence
// is skipped rather than returned: this runs as part of a teardown that must
// not fail on a file it cannot inspect, which is the behaviour the port-forward
// state removal has always had.
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

// LoadPortForwardState reads a forward's state. A missing file is reported as
// "no forward", not an error: an environment nobody opened is the ordinary case.
//
// A file naming an environment no longer in the config store reads the same
// way: deleting an environment is supposed to remove its state files (see
// RunDeleteEnvironment), but a file that predates that cleanup, or one left by
// a delete that failed partway, must not resolve as a live forward either. The
// local port range a deleted environment's file names is freed and reissued to
// whichever environment is created next, so trusting an orphaned file would
// hand back a forward that now belongs to somebody else — a stale record has
// to read as "no forward", not as a wrong one.
func LoadPortForwardState(kind, tenant, environment string) (PortForwardState, bool, error) {
	path, err := PortForwardStatePath(kind, tenant, environment)
	if err != nil {
		return PortForwardState{}, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PortForwardState{}, false, nil
		}
		return PortForwardState{}, false, err
	}
	var state PortForwardState
	if err := json.Unmarshal(data, &state); err != nil {
		return PortForwardState{}, false, fmt.Errorf("%s: %w", path, err)
	}
	configured, err := environmentIsConfigured(tenant, environment)
	if err != nil {
		return PortForwardState{}, false, err
	}
	if !configured {
		return PortForwardState{}, false, nil
	}
	return state, state.LocalPort > 0, nil
}

// environmentIsConfigured reports whether the config store still knows this
// tenant/environment. A genuine absence (ErrNotInitialized) reports false,
// nil; any other read failure is returned rather than swallowed, so a config
// read that fails for an unrelated reason (corruption, a permission error)
// is not silently misreported as "this environment was deleted" -- the same
// distinction LoadPortForwardState itself draws for a state file it cannot
// read.
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
