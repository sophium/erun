package envscope

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
)

// ScopedOpenParams maps --tenant/--environment to OpenParams; empty means the current scope.
// command names the invocation so resolution failures cite it.
func ScopedOpenParams(command, tenant, environment string) model.OpenParams {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	return model.OpenParams{
		Tenant:                    tenant,
		Environment:               environment,
		UseDefaultTenant:          tenant == "",
		UseDefaultEnvironment:     environment == "",
		Command:                   command,
		CommandScopesTenantByFlag: true,
	}
}
