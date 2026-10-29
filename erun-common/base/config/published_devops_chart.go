package config

import (
	"strings"
)

// RuntimeImageRegistry returns everything before the last "/" of runtimeImage,
// or "" for a bare name.
func RuntimeImageRegistry(runtimeImage string) string {
	ref := strings.TrimSpace(runtimeImage)
	lastSlash := strings.LastIndex(ref, "/")
	if lastSlash < 0 {
		return ""
	}
	return ref[:lastSlash]
}
