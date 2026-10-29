package runtimetarget

import (
	"fmt"

	"github.com/sophium/erun/erun-common/base/config"
)

// maxTenantNameLength is the RFC 1123 DNS label limit.
const maxTenantNameLength = 63

// ValidateTenantName requires one lowercase alphanumeric label with no hyphens,
// so the `<tenant>-<env>` namespace split stays injective. Enforced at creation only.
func ValidateTenantName(name string) error {
	if name == "" {
		return fmt.Errorf("tenant name is required")
	}
	if len(name) > maxTenantNameLength {
		return fmt.Errorf("invalid tenant name %q: must be at most %d characters", name, maxTenantNameLength)
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		return fmt.Errorf("invalid tenant name %q: tenant names must use only lowercase letters and digits with no hyphens, so the <tenant>-<env> namespace is unambiguous", name)
	}
	return nil
}

func KubernetesNamespaceName(tenant, envName string) string {
	return config.NormalizeNamespaceName(tenant + "-" + envName)
}
