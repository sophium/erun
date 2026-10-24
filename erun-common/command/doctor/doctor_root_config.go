package doctor

import (
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/orchestration"
)

// RepairOrphanedAliasParams carries the fields needed to re-init a
// provider for one orphan. Region is sourced from a referenced cloud
// context when available; SSORegion / SSOStartURL come from the
// caller because they are not derivable from any persisted state.
type RepairOrphanedAliasParams struct {
	Orphan        orchestration.OrphanedAlias
	SSORegion     string
	SSOStartURL   string
	RoleName      string
	Region        string
	OIDCIssuerURL string
	SkipLogin     bool
}

// RepairOrphanedAlias re-creates a provider for one orphaned alias by
// delegating to InitAWSCloudProvider; it stays a thin adapter so the init
// flow's SSO/identity/OIDC handling is not forked here.
func RepairOrphanedAlias(ctx core.Context, store cloud.CloudStore, params RepairOrphanedAliasParams, deps cloud.CloudDependencies) (configpkg.CloudProviderConfig, error) {
	if !params.Orphan.Parsed {
		return configpkg.CloudProviderConfig{}, fmt.Errorf("orphaned alias %q cannot be parsed; recreate the provider manually with `erun cloud init aws`", params.Orphan.Alias)
	}
	if params.Orphan.Provider != configpkg.CloudProviderAWS {
		return configpkg.CloudProviderConfig{}, fmt.Errorf("unsupported provider %q for alias %q; only %q is supported", params.Orphan.Provider, params.Orphan.Alias, configpkg.CloudProviderAWS)
	}
	initParams := cloud.InitAWSCloudProviderParams{
		Username:      params.Orphan.Username,
		AccountID:     params.Orphan.AccountID,
		SSORegion:     strings.TrimSpace(params.SSORegion),
		SSOStartURL:   strings.TrimSpace(params.SSOStartURL),
		RoleName:      strings.TrimSpace(params.RoleName),
		Region:        strings.TrimSpace(params.Region),
		OIDCIssuerURL: strings.TrimSpace(params.OIDCIssuerURL),
		SkipLogin:     params.SkipLogin,
	}
	return cloud.InitAWSCloudProvider(ctx, store, initParams, deps)
}
