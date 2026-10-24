package core

import (
	"fmt"
	"path"
	"strings"
)

// DefaultRuntimeOutputsDir must match the value the runtime chart exports.
const DefaultRuntimeOutputsDir = "/home/erun/.erun/outputs"

// RuntimeOutputsDirEnvVar names the directory an agent writes deliverables to,
// in a pod or on the host.
const RuntimeOutputsDirEnvVar = "ERUN_OUTPUTS_DIR"

// ValidateAbsolutePodPath requires an absolute pod path with no `..` segment.
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

// SanitizeOutputEntryName reduces name to one path segment so a join cannot escape.
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
