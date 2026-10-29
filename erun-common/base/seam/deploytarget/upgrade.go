package deploytarget

import (
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// UpgradeItemDeployer redeploys one lagging env to its target version.
type UpgradeItemDeployer func(ctx core.Context, item model.UpgradePlanItem) error
