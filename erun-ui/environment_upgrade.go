package main

import (
	eruncommon "github.com/sophium/erun/erun-common"
)

// ResolveUpgradePlan resolves the cross-env "Upgrade all" plan for the sidebar
// preview dialog: every opted-in environment, the channel it tracks, its
// current runtime version, and the latest version for that channel, with
// lagging members flagged and unresolved members carrying the reason.
// Read-only — it never deploys. The desktop renders the plan, the user
// confirms, and each lagging member runs its own scoped `erun upgrade` in its
// own environment.
//
// The resolver is the shared one every transport uses: the
// preview must never promise an upgrade the run will refuse, so the
// resolution policy — provenance namespace, default-image fallback only for
// an unpublished tenant image, lookup errors surfaced as unresolved — is
// decided once in erun-common, not here.
func (a *App) ResolveUpgradePlan() (eruncommon.UpgradePlan, error) {
	resolver := eruncommon.UpgradeVersionsResolverForStore(a.deps.store, a.deps.resolveImageRegistry)
	return eruncommon.BuildUpgradePlan(a.deps.store, eruncommon.UpgradeTarget{}, resolver)
}
