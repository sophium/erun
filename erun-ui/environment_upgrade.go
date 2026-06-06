package main

import eruncommon "github.com/sophium/erun/erun-common"

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
		return a.resolveRuntimeRegistryVersionsForTenant(tenant), nil
	}
	return eruncommon.BuildUpgradePlan(a.deps.store, eruncommon.UpgradeTarget{}, resolveVersions)
}
