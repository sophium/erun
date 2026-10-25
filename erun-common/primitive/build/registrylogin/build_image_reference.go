package registrylogin

import (
	"strings"
)

// IsDockerScopeDenied reports a docker token lacking write:packages, the one case that
// warrants a gh namespace-owner re-auth.
func IsDockerScopeDenied(message string) bool {
	msg := strings.ToLower(message)
	if strings.Contains(msg, "create_package") {
		return false
	}
	for _, marker := range []string{
		"does not match expected scopes",
		"permission_denied",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func DockerRegistryDisplayName(registry string) string {
	if strings.TrimSpace(registry) == "" {
		return "Docker Hub"
	}
	return registry
}
