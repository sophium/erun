package cloud

import (
	"errors"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// ErrPlatformAliasUnusable marks failures to resolve a usable erun platform alias, before
// any HTTP client exists, so callers can branch on it with errors.Is.
var ErrPlatformAliasUnusable = errors.New("erun platform alias could not be resolved")

type platformAliasUnusableError struct{ err error }

func (e platformAliasUnusableError) Error() string { return e.err.Error() }
func (e platformAliasUnusableError) Unwrap() error { return e.err }
func (e platformAliasUnusableError) Is(target error) bool {
	return target == ErrPlatformAliasUnusable
}

func markPlatformAliasUnusable(err error) error {
	if err == nil {
		return nil
	}
	return platformAliasUnusableError{err: err}
}

// ErrPlatformAliasAmbiguous marks several erun aliases configured with none selected,
// whose remedy is selecting one rather than adding another.
var ErrPlatformAliasAmbiguous = errors.New("multiple erun platform cloud provider aliases are configured")

// platformAliasAmbiguityError keeps the transport-specific message and adds the classification.
type platformAliasAmbiguityError struct{ err error }

func (e platformAliasAmbiguityError) Error() string { return e.err.Error() }
func (e platformAliasAmbiguityError) Unwrap() error { return e.err }
func (e platformAliasAmbiguityError) Is(target error) bool {
	return target == ErrPlatformAliasAmbiguous
}

func platformAliasAmbiguity(err error) error { return platformAliasAmbiguityError{err: err} }

// platformAliasSelection is the alias-selecting argument as spelled by the calling transport.
type platformAliasSelection string

const (
	// PlatformAliasSelectionCLIFlag is the CLI's --erun-alias flag.
	PlatformAliasSelectionCLIFlag  platformAliasSelection = "--erun-alias"
	platformAliasSelectionMCPParam platformAliasSelection = "alias"
)

// platformAliasSelectionFor returns the MCP param for MCP tool calls and the CLI flag otherwise.
func platformAliasSelectionFor(ctx core.Context) platformAliasSelection {
	if strings.TrimSpace(ctx.MCPTool) != "" {
		return platformAliasSelectionMCPParam
	}
	return PlatformAliasSelectionCLIFlag
}

// unresolvedPlatformAliasError names both causes (none configured, several unselected)
// because the caller cannot tell them apart.
func unresolvedPlatformAliasError(selection platformAliasSelection) error {
	if selection == platformAliasSelectionMCPParam {
		return fmt.Errorf("no usable erun platform alias could be resolved: either no erun-type alias is configured, or several are and none was selected; pass %s to choose one, or run `erun cloud init erun --api-url <url>` when none exists", selection)
	}
	return fmt.Errorf("no erun platform cloud provider alias is configured; run `erun cloud init erun --api-url <url>` first")
}

// ResolveERunPlatformAlias resolves the erun platform alias, naming selection in errors.
func ResolveERunPlatformAlias(store configpkg.CloudReadStore, alias string, selection platformAliasSelection) (configpkg.CloudProviderConfig, error) {
	alias = strings.TrimSpace(alias)
	if alias != "" {
		provider, err := ResolveCloudProvider(store, alias)
		if err != nil {
			return configpkg.CloudProviderConfig{}, markPlatformAliasUnusable(err)
		}
		if provider.Provider != configpkg.CloudProviderERun {
			return configpkg.CloudProviderConfig{}, markPlatformAliasUnusable(fmt.Errorf("cloud provider alias %q is a %q-type alias, not an erun platform alias", provider.Alias, provider.Provider))
		}
		return provider, nil
	}
	providers, err := ListCloudProviders(store)
	if err != nil {
		return configpkg.CloudProviderConfig{}, markPlatformAliasUnusable(err)
	}
	erunProviders := make([]configpkg.CloudProviderConfig, 0, len(providers))
	for _, provider := range providers {
		if provider.Provider == configpkg.CloudProviderERun {
			erunProviders = append(erunProviders, provider)
		}
	}
	switch len(erunProviders) {
	case 0:
		return configpkg.CloudProviderConfig{}, markPlatformAliasUnusable(unresolvedPlatformAliasError(selection))
	case 1:
		return erunProviders[0], nil
	default:
		return configpkg.CloudProviderConfig{}, markPlatformAliasUnusable(platformAliasAmbiguity(
			fmt.Errorf("multiple erun platform cloud provider aliases are configured; pass %s to choose one", selection)))
	}
}

// NewPlatformClientForAlias builds a PlatformClient that mints a bearer token per call from the alias.
func NewPlatformClientForAlias(ctx core.Context, store configpkg.CloudReadStore, alias string, deps CloudDependencies) (*platformclient.PlatformClient, configpkg.CloudProviderConfig, error) {
	provider, err := ResolveERunPlatformAlias(store, alias, platformAliasSelectionFor(ctx))
	if err != nil {
		return nil, configpkg.CloudProviderConfig{}, err
	}
	if provider.ERun == nil || strings.TrimSpace(provider.ERun.APIURL) == "" {
		// Provider is set but the ERun block is missing: init writes both, so this is a
		// truncated config; point at re-login, not `cloud init`.
		return nil, configpkg.CloudProviderConfig{}, markPlatformAliasUnusable(fmt.Errorf("erun platform alias %q is incomplete (its erun api configuration is missing, likely dropped by a config write from an older erun component); run `erun cloud login %s` to restore it", provider.Alias, provider.Alias))
	}
	client := platformclient.NewPlatformClient(provider.ERun.APIURL, func() (string, error) {
		token, err := CloudProviderBearerToken(ctx, store, CloudBearerParams{Alias: provider.Alias}, deps)
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
func TracePlatformCall(ctx core.Context, provider configpkg.CloudProviderConfig, method, path string, details ...string) {
	line := fmt.Sprintf("platform: %s %s%s", method, provider.ERun.APIURL, path)
	if len(details) > 0 {
		line += " (" + strings.Join(details, ", ") + ")"
	}
	ctx.Trace(line)
}
