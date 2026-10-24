package config

import (
	"strings"
)

// RuntimeImageRegistry returns the registry prefix (host and any org path) of a
// runtime image reference, or "" when the reference is a bare image name with no
// registry. The image name and any tag/digest live in the segment after the last
// "/", so everything before it is the registry — e.g. ghcr.io/sophium/erun-devops
// and ghcr.io/sophium/erun-devops:1.0.149 both yield ghcr.io/sophium, while a bare
// "erun-devops" yields "".
func RuntimeImageRegistry(runtimeImage string) string {
	ref := strings.TrimSpace(runtimeImage)
	lastSlash := strings.LastIndex(ref, "/")
	if lastSlash < 0 {
		return ""
	}
	return ref[:lastSlash]
}
