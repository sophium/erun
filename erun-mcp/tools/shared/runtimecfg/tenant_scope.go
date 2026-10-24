package runtimecfg

import (
	"strings"
)

// ScopedTenantEnv confines the server to its own tenant/environment. Unlike
// ResolveLocalTarget, an unresolved empty pair passes through.
func ScopedTenantEnv(inputTenant, inputEnv string, runtime RuntimeConfig) (tenant, environment string, err error) {
	return matchOrDefaultLocalTarget(
		strings.TrimSpace(runtime.Context.Tenant), strings.TrimSpace(runtime.Context.Environment),
		inputTenant, inputEnv,
	)
}
