package eruncommon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
// indistinguishable on a case-insensitive volume.
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
		if err := validateStatePathSegment(segment.kind, segment.value); err != nil {
			return "", err
		}
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, configRoot, "portforward", kind, tenant, environment+".json"), nil
}

// PortForwardLogPathForState is where a forward's kubectl output accumulates:
// the state-file path with its extension replaced, which is exactly how the
// state record's own logPath field is derived when it is written. Both
// directions go through this one spelling, because a reader that derived the
// log differently from the writer would look for a file the writer never
// touched.
//
// It takes the state path rather than the record's identity so the migration
// off the legacy cache-dir location can derive the log beside a path that is
// not (yet) the canonical one.
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
func RemovePortForwardRecord(ctx Context, kind, tenant, environment string) error {
	statePath, err := PortForwardStatePath(kind, tenant, environment)
	if err != nil {
		return err
	}
	// Derived from the state path, never read from the record's own logPath:
	// a hand-edited or half-written record must not be able to point the
	// removal at a file outside the forward tree.
	logPath := PortForwardLogPathForState(statePath)
	if record, ok := readPortForwardStateFile(statePath); ok && isProcessAlive(record.ProcessID) {
		ctx.Trace(fmt.Sprintf("%s: keeping port-forward log %s: the forward that recorded it (PID %d) is still running", kind, logPath, record.ProcessID))
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
func removeFileIfPresent(ctx Context, path string) error {
	if _, err := os.Stat(path); err != nil {
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
	_, _, err := ConfigStore{}.LoadEnvConfig(tenant, environment)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrNotInitialized) {
		return false, nil
	}
	return false, err
}
