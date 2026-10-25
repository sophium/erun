package cloudconfig

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
)

// ResolveERunPlatformAlias returns the explicit erun alias, or the sole configured one.
// Local config only, so safe under --dry-run.
func ResolveERunPlatformAlias(store configpkg.CloudReadStore, alias string) (model.CloudProviderConfig, error) {
	return providerconfig.ResolveERunPlatformAlias(store, alias, providerconfig.PlatformAliasSelectionCLIFlag)
}
