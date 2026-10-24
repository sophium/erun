package main

import (
	"github.com/sophium/erun/erun-common/command/lifecycle"
)

// ResolveUpgradePlan builds the read-only "Upgrade all" preview using the shared
// resolver, so it never promises what the run would refuse.
func (a *App) ResolveUpgradePlan() (lifecycle.UpgradePlan, error) {
	resolver := lifecycle.UpgradeVersionsResolverForStore(a.deps.store, a.deps.resolveImageRegistry)
	return lifecycle.BuildUpgradePlan(a.deps.store, lifecycle.UpgradeTarget{}, resolver)
}
