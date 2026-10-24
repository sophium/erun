package provision

import (
	"context"
	"fmt"

	"github.com/sophium/erun/erun-common/base/config"
)

// TenantRuntimeImage is the tenant's own <tenant>-devops runtime image.
func TenantRuntimeImage(registry, tenant, version string) string {
	return fmt.Sprintf("%s/%s-devops:%s", registry, tenant, version)
}

// CanonicalRuntimeImage is the published erun-devops fallback image.
func CanonicalRuntimeImage(registry, version string) string {
	return fmt.Sprintf("%s/%s:%s", registry, config.DevopsComponentName, version)
}

// ResolveRuntimeImage returns the tenant's image unless the registry confirms it
// was never published; a nil checker or inconclusive probe keeps it.
func ResolveRuntimeImage(ctx context.Context, checker RuntimeImageChecker, registry, tenant, version string) (image string, bootstrap bool) {
	tenantImage := TenantRuntimeImage(registry, tenant, version)
	if checker == nil {
		return tenantImage, false
	}
	canonical := CanonicalRuntimeImage(registry, version)
	missing, err := checker.ConfirmedMissing(ctx, tenantImage, canonical)
	if err != nil || !missing {
		return tenantImage, false
	}
	return canonical, true
}
