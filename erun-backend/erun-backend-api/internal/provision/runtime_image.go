package provision

import (
	"context"
	"fmt"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TenantRuntimeImage is the tenant's own <tenant>-devops runtime image at a
// version, pulled from the configured registry — the image every Job that
// runs the tenant's erun toolchain (deploy, stop, delete, release) prefers.
func TenantRuntimeImage(registry, tenant, version string) string {
	return fmt.Sprintf("%s/%s-devops:%s", registry, tenant, version)
}

// CanonicalRuntimeImage is the published erun-devops image every such Job
// falls back to when the tenant has never published its own <tenant>-devops
// image — the same canonical image `erun deploy --runtime-image` installs on
// an operator's own machine.
func CanonicalRuntimeImage(registry, version string) string {
	return fmt.Sprintf("%s/%s:%s", registry, eruncommon.DevopsComponentName, version)
}

// ResolveRuntimeImage is the one decision every Job that runs a tenant's erun
// toolchain applies: run the tenant's own image, unless the registry
// affirmatively confirms it was never published, in which case fall back to
// the canonical image. Before this, only the deploy Job carried the fallback —
// stop, delete, and the release-queue runner all kept naming an image that was
// never published, so a bootstrapped environment (created successfully on the
// canonical fallback) could never be torn down: its delete Job could only
// ImagePullBackOff on the tenant image that confirmed-missing check already
// knew did not exist. A nil checker, or an inconclusive probe, keeps the
// tenant's own image — RuntimeImageChecker's fail-open contract.
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
