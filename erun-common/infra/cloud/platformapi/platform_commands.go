package platformapi

import (
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
	"github.com/sophium/erun/erun-common/infra/cloud/providertoken"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// NewPlatformClientForAlias builds a PlatformClient that mints a bearer token per call from the alias.
func NewPlatformClientForAlias(ctx core.Context, store configpkg.CloudReadStore, alias string, deps cloud.CloudDependencies) (*platformclient.PlatformClient, model.CloudProviderConfig, error) {
	provider, err := providerconfig.ResolveERunPlatformAlias(store, alias, providerconfig.PlatformAliasSelectionFor(ctx))
	if err != nil {
		return nil, model.CloudProviderConfig{}, err
	}
	if provider.ERun == nil || strings.TrimSpace(provider.ERun.APIURL) == "" {
		// Provider is set but the ERun block is missing: init writes both, so this is a
		// truncated config; point at re-login, not `cloud init`.
		return nil, model.CloudProviderConfig{}, providerconfig.MarkPlatformAliasUnusable(fmt.Errorf("erun platform alias %q is incomplete (its erun api configuration is missing, likely dropped by a config write from an older erun component); run `erun cloud login %s` to restore it", provider.Alias, provider.Alias))
	}
	client := platformclient.NewPlatformClient(provider.ERun.APIURL, func() (string, error) {
		token, err := providertoken.CloudProviderBearerToken(ctx, store, model.CloudBearerParams{Alias: provider.Alias}, deps)
		if err != nil {
			return "", err
		}
		return token.Token, nil
	})
	if ctx.MCPTool != "" {
		client = client.WithMCPTool(ctx.MCPTool)
	}
	return client, provider, nil
}

// TracePlatformCall is the audit line traced before a platform HTTP call; callers skip
// the call under ctx.DryRun.
func TracePlatformCall(ctx core.Context, provider model.CloudProviderConfig, method, path string, details ...string) {
	line := fmt.Sprintf("platform: %s %s%s", method, provider.ERun.APIURL, path)
	if len(details) > 0 {
		line += " (" + strings.Join(details, ", ") + ")"
	}
	ctx.Trace(line)
}
