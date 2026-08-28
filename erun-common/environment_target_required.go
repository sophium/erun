package eruncommon

import (
	"fmt"
	"strings"
)

// errMissingTenantOrEnvironment names which of tenant/environment an
// erun-common call was given empty, the operation that needed them, and the
// fix -- the same shape as erun-mcp's errMissingLocalTarget, adapted for a
// library function instead of a bound server: erun-common never resolves a
// tenant/environment target from ambient state (this package is called
// directly as a plain library by all three transports), so a blank value
// here means the caller -- a CLI flag, an MCP tool argument, or a desktop
// binding -- did not resolve one before calling in. Returns nil when both
// are set, so callers can use it as their whole guard clause.
func errMissingTenantOrEnvironment(operation, tenant, environment string) error {
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
