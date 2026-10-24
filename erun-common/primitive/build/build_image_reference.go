package build

import (
	"strings"
)

// IsDockerScopeDenied reports the GitHub scope-mismatch case where docker's
// login token lacks write:packages. It stays deliberately narrow — distinct
// from the org-policy create_package denial and from missing credentials a
// re-login fixes — because only this case warrants a namespace-owner re-auth
// (TryGHCRNamespaceLogin) via gh; a prompt-driven docker login cannot change
// which token docker already holds.
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
