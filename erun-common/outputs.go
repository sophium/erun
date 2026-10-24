package eruncommon

import (
	"fmt"
	"path"
	"strings"
)

// DefaultRuntimeOutputsDir is the canonical agent outputs directory inside the
// runtime pod. The CLI runs off the pod and can't read the pod's env, so this
// host-side constant must stay in sync with the value the runtime chart exports.
const DefaultRuntimeOutputsDir = "/home/erun/.erun/outputs"

// OutputEntry is one file or directory in the agent outputs directory.
// RuntimeOutputsDirEnvVar names the directory an agent writes its deliverables
// to. It is the one convention shared by both kinds of agent: in a pod it points
// at the runtime outputs dir, and for a host-side orchestrator at that
// orchestrator's own host directory.
const RuntimeOutputsDirEnvVar = "ERUN_OUTPUTS_DIR"

// ValidateAbsolutePodPath is shared by outputs and inputs: both name a path
// inside the runtime pod's filesystem and must be absolute with no `..`
// traversal segment.
func ValidateAbsolutePodPath(value, label string) (string, error) {
	if !strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("%s must be absolute: %q", label, value)
	}
	cleaned := path.Clean(value)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return "", fmt.Errorf("%s must not contain '..': %q", label, value)
	}
	return cleaned, nil
}

// SanitizeOutputEntryName reduces an entry name to a single path segment so
// path.Join with it can never escape the base directory.
func SanitizeOutputEntryName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if i := strings.LastIndexAny(trimmed, "/\\"); i >= 0 {
		trimmed = trimmed[i+1:]
	}
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" || trimmed == "." || trimmed == ".." {
		return "", fmt.Errorf("invalid outputs entry name: %q", name)
	}
	return trimmed, nil
}
