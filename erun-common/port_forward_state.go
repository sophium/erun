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

// ERunStateDirName is the canonical spelling of the per-install state directory
// under os.UserConfigDir(), shared by every writer of that tree. The spelling is
// load-bearing: on a case-sensitive volume a second spelling ("erun") resolves to
// a sibling directory rather than to the same one, so the tree splits and state
// written under one spelling reads as absent under the other. A case-insensitive
// volume hides the split, which is why the divergent writer is what has to be
// pinned rather than any single reader.
const ERunStateDirName = "ERun"

// legacyERunStateDirName is the lowercase spelling a former writer used. It is
// read and moved forward, never written to.
const legacyERunStateDirName = "erun"

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
func PortForwardStatePath(kind, tenant, environment string) (string, error) {
	return portForwardStatePathInDir(ERunStateDirName, kind, tenant, environment)
}

// LegacyPortForwardStatePath is the same state file under the lowercase spelling
// a former writer used. It is exported because a dry run reports where the state
// actually lives today without moving the operator's file on disk.
func LegacyPortForwardStatePath(kind, tenant, environment string) (string, error) {
	return portForwardStatePathInDir(legacyERunStateDirName, kind, tenant, environment)
}

func portForwardStatePathInDir(stateDir, kind, tenant, environment string) (string, error) {
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
	return filepath.Join(configDir, stateDir, "portforward", kind, tenant, environment+".json"), nil
}

// PortForwardStatePaths returns every location a forward's record could occupy:
// the canonical one, plus the legacy lowercase spelling when that is a different
// file. Callers that remove a record entirely (removePortForwardStateFiles) need
// both: the CLI reads a forward's state without the configured-environment guard
// the shared reader applies, so a record left under the legacy spelling would
// resolve as a live forward for an environment that was deleted.
func PortForwardStatePaths(kind, tenant, environment string) ([]string, error) {
	canonical, err := PortForwardStatePath(kind, tenant, environment)
	if err != nil {
		return nil, err
	}
	legacy, err := LegacyPortForwardStatePath(kind, tenant, environment)
	if err != nil || legacy == canonical {
		return []string{canonical}, nil
	}
	return []string{canonical, legacy}, nil
}

// MigrateLegacyPortForwardState moves a forward's record out of the lowercase
// directory into the canonical one and returns the canonical path, so state an
// older writer left behind is carried forward instead of reading as "no forward"
// from the moment the spelling was unified.
//
// A canonical record that already exists wins and nothing is moved: it is the
// newer one, and a legacy file beside it is stale. On a case-insensitive volume
// the canonical stat resolves the legacy file itself, so that is the ordinary
// path there and the rename never runs.
//
// A move that fails leaves the legacy record in place and still returns the
// canonical path: the caller writes its own record there, and the read fallback
// covers the file that could not be moved.
func MigrateLegacyPortForwardState(kind, tenant, environment string) (string, error) {
	canonical, err := PortForwardStatePath(kind, tenant, environment)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(canonical); err == nil {
		return canonical, nil
	}
	legacy, err := LegacyPortForwardStatePath(kind, tenant, environment)
	if err != nil || legacy == canonical {
		return canonical, nil
	}
	if _, err := os.Stat(legacy); err != nil {
		return canonical, nil
	}
	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		return canonical, nil
	}
	if err := os.Rename(legacy, canonical); err != nil {
		return canonical, nil
	}
	_ = os.Rename(portForwardSiblingLogPath(legacy), portForwardSiblingLogPath(canonical))
	return canonical, nil
}

// portForwardSiblingLogPath is the log a forward keeps beside its state file.
// The log is named for the state path, so a state file that moves takes its log
// with it and the record's own LogPath stays consistent with where it now lives.
func portForwardSiblingLogPath(statePath string) string {
	return strings.TrimSuffix(statePath, filepath.Ext(statePath)) + ".log"
}

// LoadPortForwardState reads a forward's state. A missing file is reported as
// "no forward", not an error: an environment nobody opened is the ordinary case.
// A record left under the legacy lowercase spelling is moved forward and read
// from there rather than reported as missing.
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
	path, err := MigrateLegacyPortForwardState(kind, tenant, environment)
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
