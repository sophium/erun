package main

import (
	"github.com/sophium/erun/erun-common/command/lifecycle"
)

// ResolveUpgradePlan builds the read-only cross-env "Upgrade all" preview for
// the sidebar; it never deploys. It resolves versions through the shared
// resolver so the preview never promises an upgrade the actual run would refuse.
func (a *App) ResolveUpgradePlan() (lifecycle.UpgradePlan, error) {
	resolver := lifecycle.UpgradeVersionsResolverForStore(a.deps.store, a.deps.resolveImageRegistry)
	return lifecycle.BuildUpgradePlan(a.deps.store, lifecycle.UpgradeTarget{}, resolver)
}
