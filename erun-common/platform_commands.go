package eruncommon

import (
	"errors"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// IsPlatformAliasAmbiguity reports whether err is that ambiguity, through any
// markPlatformAliasUnusable the resolver applied on the way out.
func IsPlatformAliasAmbiguity(err error) bool {
	return errors.Is(err, cloud.ErrPlatformAliasAmbiguous)
}

// ResolveERunPlatformAlias resolves which "erun"-type cloud provider alias a
// platform command targets: the explicit alias when given (verified to be an
// erun-type alias), or the caller's sole configured erun alias when exactly
// one exists. Local config lookup only — never touches the network, so it is
// always safe to call in --dry-run mode.
func ResolveERunPlatformAlias(store configpkg.CloudReadStore, alias string) (configpkg.CloudProviderConfig, error) {
	return cloud.ResolveERunPlatformAlias(store, alias, cloud.PlatformAliasSelectionCLIFlag)
}
