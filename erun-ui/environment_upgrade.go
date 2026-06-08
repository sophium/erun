package main

import (
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// ResolveUpgradePlan resolves the cross-env "Upgrade all" plan for the sidebar
// preview dialog: every opted-in environment, the channel it tracks, its
// current runtime version, and the latest version for that channel (resolved
// from the runtime image registry), with lagging members flagged. Read-only —
// it never deploys. The desktop renders the plan, the user confirms, and
// StartUpgradeAllSession runs the actual `erun upgrade`.
func (a *App) ResolveUpgradePlan() (eruncommon.UpgradePlan, error) {
	resolveVersions := func(_ eruncommon.Context, tenant string) (eruncommon.RuntimeRegistryVersions, error) {
		// Honor the shared version-override test seam first (so the plan is
		// resolvable offline for verification), then the injected registry dep.
		if versions, ok := eruncommon.RuntimeVersionsOverrideFromEnv(); ok {
			return versions, nil
		}
		return a.resolveUpgradeRegistryVersionsForTenant(tenant), nil
	}
	return eruncommon.BuildUpgradePlan(a.deps.store, eruncommon.UpgradeTarget{}, resolveVersions)
}

// resolveUpgradeRegistryVersionsForTenant resolves a tenant's channel versions
// for the Upgrade-all plan, mirroring the Runtime "Version to deploy" picker
// (runtimeVersionSuggestions): when the tenant-specific runtime image is not
// available for a channel, fall back to the default ERun image (erun-devops).
// Without this fallback an env that tracks a channel whose tenant image was
// never published resolves to an empty target and is reported as having no
// upgrade — even though the picker offers the ERun image's version for the same
// env. Reuses resolveRuntimeRegistryVersionsForTenant for both lookups.
func (a *App) resolveUpgradeRegistryVersionsForTenant(tenant string) eruncommon.RuntimeRegistryVersions {
	versions := a.resolveRuntimeRegistryVersionsForTenant(a.runtimeRegistryNamespace(tenant, ""), tenant)
	// The ERun tenant already resolves the default image; nothing to fall back
	// to, and an empty result is genuinely "no versions".
	if strings.TrimSpace(tenant) == "" ||
		eruncommon.RuntimeReleaseName(tenant) == eruncommon.DefaultRuntimeImageName {
		return versions
	}
	if strings.TrimSpace(versions.LatestStable) != "" && strings.TrimSpace(versions.LatestSnapshot) != "" {
		return versions
	}
	fallback := a.resolveRuntimeRegistryVersionsForTenant("", "")
	if strings.TrimSpace(versions.LatestStable) == "" {
		versions.LatestStable = fallback.LatestStable
	}
	if strings.TrimSpace(versions.LatestSnapshot) == "" {
		versions.LatestSnapshot = fallback.LatestSnapshot
	}
	return versions
}
