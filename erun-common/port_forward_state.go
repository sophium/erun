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
func PortForwardStatePath(kind, tenant, environment string) (string, error) {
	kind = strings.TrimSpace(kind)
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if kind == "" || tenant == "" || environment == "" {
		return "", fmt.Errorf("kind, tenant and environment are required")
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "erun", "portforward", kind, tenant, environment+".json"), nil
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
