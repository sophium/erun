package erunmcp

import (
	"fmt"
	"strings"
)

// scopedTenantEnv pins a tenant-scoped tool to this per-env MCP server's own
// environment (#657). A per-env erun-mcp server runs in one tenant's namespace
// and operates only on its own tenant/environment, so a caller-supplied
// tenant/environment that differs from the pod's identity is refused rather than
// silently honored — closing the gap where a `tenant`/`environment` argument
// overrode the pod's own identity. Empty inputs default to the pod's identity;
// when the pod has no identity (unconfigured/local), the caller's value is used.
func scopedTenantEnv(inputTenant, inputEnv string, runtime RuntimeConfig) (tenant, environment string, err error) {
	tenant = strings.TrimSpace(runtime.Context.Tenant)
	environment = strings.TrimSpace(runtime.Context.Environment)
	if t := strings.TrimSpace(inputTenant); t != "" && tenant != "" && t != tenant {
		return "", "", fmt.Errorf("this MCP server is scoped to tenant %q; refusing to operate on tenant %q", tenant, t)
	}
	if e := strings.TrimSpace(inputEnv); e != "" && environment != "" && e != environment {
		return "", "", fmt.Errorf("this MCP server is scoped to environment %q; refusing to operate on environment %q", environment, e)
	}
	if tenant == "" {
		tenant = strings.TrimSpace(inputTenant)
	}
	if environment == "" {
		environment = strings.TrimSpace(inputEnv)
	}
	return tenant, environment, nil
}
