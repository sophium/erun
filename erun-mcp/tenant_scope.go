package erunmcp

import "strings"

// scopedTenantEnv confines a per-env MCP server to its own tenant/environment: a
// caller-supplied tenant/environment that differs from the pod's identity is
// refused rather than silently honored (see matchOrDefaultLocalTarget, which
// resolveLocalTarget also builds on). Unlike resolveLocalTarget, an unresolved
// target -- both server and caller silent -- is not an error here: some
// callers (doctor's root-config-only actions) have no tenant/environment work
// to do at all, so the empty pair simply passes through.
func scopedTenantEnv(inputTenant, inputEnv string, runtime RuntimeConfig) (tenant, environment string, err error) {
	return matchOrDefaultLocalTarget(
		strings.TrimSpace(runtime.Context.Tenant), strings.TrimSpace(runtime.Context.Environment),
		inputTenant, inputEnv,
	)
}
