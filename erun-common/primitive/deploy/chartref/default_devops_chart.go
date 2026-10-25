package chartref

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
)

// RuntimeComponentNames lists candidate runtime chart names, most specific first.
func RuntimeComponentNames(tenant string) []string {
	names := []string{config.DevopsComponentName}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return names
	}

	tenantComponent := tenant + "-devops"
	if tenantComponent == config.DevopsComponentName {
		return names
	}
	return append([]string{tenantComponent}, names...)
}
