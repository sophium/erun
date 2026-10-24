package shared

import (
	"strings"
)

// MissingTenantOrEnvironmentFlags names which of the shared --tenant/--environment
// flags a caller left empty, for validateActivityTarget/validateJobTarget to build
// an operation-specific refusal instead of a bare "tenant and environment are
// required" dead end.
func MissingTenantOrEnvironmentFlags(tenant, environment string) []string {
	var missing []string
	if strings.TrimSpace(tenant) == "" {
		missing = append(missing, "tenant")
	}
	if strings.TrimSpace(environment) == "" {
		missing = append(missing, "environment")
	}
	return missing
}
