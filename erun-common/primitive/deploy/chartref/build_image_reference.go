package chartref

import (
	"regexp"
	"strings"
)

// dockerVersionRegistryPattern catches .Chart.AppVersion leaking in as a registry.
var dockerVersionRegistryPattern = regexp.MustCompile(`^\d+\.\d+\.\d+`)

func dockerRegistryLooksLikeVersion(registry string) bool {
	registry = strings.TrimSpace(registry)
	if registry == "" {
		return false
	}
	// Explicit port (e.g. "localhost:5000" or "192.168.1.1:5000") → real registry.
	if strings.Contains(registry, ":") {
		return false
	}
	return dockerVersionRegistryPattern.MatchString(registry)
}
