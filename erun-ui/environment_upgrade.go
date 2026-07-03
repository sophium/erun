package main

import (
	eruncommon "github.com/sophium/erun/erun-common"
)

// ResolveUpgradePlan builds the read-only cross-env "Upgrade all" preview for
// the sidebar; it never deploys. It resolves versions through the shared
// resolver so the preview never promises an upgrade the actual run would refuse.
func (a *App) ResolveUpgradePlan() (eruncommon.UpgradePlan, error) {
	resolver := eruncommon.UpgradeVersionsResolverForStore(a.deps.store, a.deps.resolveImageRegistry)
	return eruncommon.BuildUpgradePlan(a.deps.store, eruncommon.UpgradeTarget{}, resolver)
}
