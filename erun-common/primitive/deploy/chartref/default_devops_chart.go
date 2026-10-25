package chartref

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
)

// RuntimeComponentNames lists candidate runtime chart names, most specific first.
func RuntimeComponentNames(tenant string) []string {
	names := []string{runtimetarget.DevopsComponentName}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return names
	}

	tenantComponent := tenant + "-devops"
	if tenantComponent == runtimetarget.DevopsComponentName {
		return names
	}
	return append([]string{tenantComponent}, names...)
}
