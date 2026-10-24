package core

import (
	"fmt"
	"strings"
)

// ErrMissingTenantOrEnvironment reports which of tenant/environment is empty;
// erun-common never resolves them from ambient state. Nil when both are set.
func ErrMissingTenantOrEnvironment(operation, tenant, environment string) error {
	var missing []string
	if strings.TrimSpace(tenant) == "" {
		missing = append(missing, "tenant")
	}
	if strings.TrimSpace(environment) == "" {
		missing = append(missing, "environment")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(
		"%s: %s not set -- erun-common does not resolve a tenant/environment target from ambient state, so the caller must pass both explicitly",
		operation, strings.Join(missing, " and "),
	)
}
