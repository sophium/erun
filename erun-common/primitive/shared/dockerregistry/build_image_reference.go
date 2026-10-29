package dockerregistry

import (
	"strings"
)

func IsDockerPushAuthorizationError(message string) bool {
	message = strings.ToLower(message)
	for _, marker := range []string{
		"insufficient_scope",
		"authorization failed",
		"unauthorized",
		"access denied",
		"requested access to the resource is denied",
		"no basic auth credentials",
		"error from registry: denied",
		"denied: denied",
		"permission_denied",
		"does not match expected scopes",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func IsDockerCreatePackageDenied(message string) bool {
	return strings.Contains(strings.ToLower(message), "create_package")
}

// IsDockerUnknownBlobError matches a registry refusing a manifest whose layer
// docker skipped as already pushed.
func IsDockerUnknownBlobError(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "unknown blob") || strings.Contains(message, "blob_unknown")
}
