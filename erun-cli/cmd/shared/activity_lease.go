package shared

import (
	"strings"
)

// MissingTenantOrEnvironmentFlags names which of --tenant/--environment were left empty.
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
