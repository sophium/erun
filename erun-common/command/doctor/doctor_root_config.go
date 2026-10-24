package doctor

import (
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// RepairOrphanedAliasParams carries the fields needed to re-init one orphan's provider.
type RepairOrphanedAliasParams struct {
	Orphan        configpkg.OrphanedAlias
	SSORegion     string
	SSOStartURL   string
	RoleName      string
	Region        string
	OIDCIssuerURL string
	SkipLogin     bool
}

// RepairOrphanedAlias re-creates a provider for one orphaned alias via InitAWSCloudProvider.
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
