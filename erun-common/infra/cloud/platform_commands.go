package cloud

import (
	"errors"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// platform_commands.go is the shared planning/execution layer `erun platform`
// (CLI) and its MCP tools both drive: it resolves which erun-hosted-platform
// cloud alias a call targets, builds a PlatformClient that mints a fresh
// bearer token per call from that alias, and traces the resolved HTTP call so
// --dry-run (CLI) and a preview path (MCP) never need to reach the network.

// ErrPlatformAliasUnusable is the sentinel a caller checks via errors.Is to
// tell "this call cannot even resolve a usable erun platform alias" (none
// configured, an incomplete one, or the wrong alias type) apart from a
// failure that only happens once a client already exists (a network error,
// a non-2xx from the platform, a refresh token that can no longer mint a
// bearer token). ResolveERunPlatformAlias and newPlatformClientForAlias wrap
// every error they return with this before any HTTP client is built, so a
// caller that only checks the process exit code -- the one channel that
// survives a script redirecting stderr away, unlike improving the error text
// -- can still branch on it.
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

// ErrPlatformAliasAmbiguous marks the one alias-resolution failure that names a
// choice rather than a gap: several erun-type aliases are configured and none
// was selected. Its remedy is the selection itself, so a caller that only knows
// the call failed -- and reports the zero-alias remedy, which adds a fourth
// alias to a config that already had too many -- is answering a question it
// cannot tell apart. Reading the marker is how a caller distinguishes them
// without matching on wording, which the transports vary by name.
var ErrPlatformAliasAmbiguous = errors.New("multiple erun platform cloud provider aliases are configured")

// platformAliasAmbiguityError marks the ambiguity while leaving the message the
// caller rendered it with -- the selection is spelled per transport -- so the
// operator-facing text is unchanged and the classification travels alongside it.
type platformAliasAmbiguityError struct{ err error }

func (e platformAliasAmbiguityError) Error() string { return e.err.Error() }
func (e platformAliasAmbiguityError) Unwrap() error { return e.err }
func (e platformAliasAmbiguityError) Is(target error) bool {
	return target == ErrPlatformAliasAmbiguous
}

func platformAliasAmbiguity(err error) error { return platformAliasAmbiguityError{err: err} }

// platformAliasSelection names, in the vocabulary of the transport the
// operator is actually using, the argument that picks one alias out of
// several. The CLI and the MCP server resolve aliases through this same
// function, but their operators make that choice differently: a remedy that
// spells it the other transport's way is not something the reader can type.
type platformAliasSelection string

const (
	// PlatformAliasSelectionCLIFlag is the CLI's persistent --erun-alias flag.
	PlatformAliasSelectionCLIFlag platformAliasSelection = "--erun-alias"
	// platformAliasSelectionMCPParam is the MCP platform tools' `alias` input.
	platformAliasSelectionMCPParam platformAliasSelection = "alias"
)

// selectionArgument reports which spelling the calling transport's operator
// must use. An MCP tool call carries the tool name; the CLI and the desktop UI
// do not, and both select with the flag.
func platformAliasSelectionFor(ctx core.Context) platformAliasSelection {
	if strings.TrimSpace(ctx.MCPTool) != "" {
		return platformAliasSelectionMCPParam
	}
	return PlatformAliasSelectionCLIFlag
}

// unresolvedPlatformAliasError is the message for "no alias was given and none
// could be singled out". The count that produced it is observation; the reason
// it is zero is not. Naming the zero case alone as the cause -- and its remedy
// as the fix -- tells an operator whose config actually holds several aliases
// to add yet another, deepening the ambiguity that stopped the call. So the
// message names both causes it cannot tell apart and gives the next action for
// each, rather than picking one and being wrong half the time.
func unresolvedPlatformAliasError(selection platformAliasSelection) error {
	if selection == platformAliasSelectionMCPParam {
		return fmt.Errorf("no usable erun platform alias could be resolved: either no erun-type alias is configured, or several are and none was selected; pass %s to choose one, or run `erun cloud init erun --api-url <url>` when none exists", selection)
	}
	return fmt.Errorf("no erun platform cloud provider alias is configured; run `erun cloud init erun --api-url <url>` first")
}

// ResolveERunPlatformAlias is ResolveERunPlatformAlias with the selection
// argument spelled for the caller's own transport.
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

// NewPlatformClientForAlias resolves the erun platform alias and builds a
// PlatformClient against it, minting a fresh bearer token per call via the
// alias's stored refresh/access token.
func NewPlatformClientForAlias(ctx core.Context, store configpkg.CloudReadStore, alias string, deps CloudDependencies) (*platformclient.PlatformClient, configpkg.CloudProviderConfig, error) {
	provider, err := ResolveERunPlatformAlias(store, alias, platformAliasSelectionFor(ctx))
	if err != nil {
		return nil, configpkg.CloudProviderConfig{}, err
	}
	if provider.ERun == nil || strings.TrimSpace(provider.ERun.APIURL) == "" {
		// A nil/empty ERun block on an alias whose Provider is already
		// CloudProviderERun is not "never configured" — init always writes it
		// together with Provider — it is incomplete: most likely truncated by a
		// config.yaml write from a component that doesn't know this field exists
		// Point at re-login rather than `cloud init`, which would
		// read as "start over" and paper over the real defect.
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

// TracePlatformCall is the single audit line every platform command traces
// before it would send its HTTP request, satisfying the dry-run contract:
// callers skip the real client.* call under ctx.DryRun once this has traced
// the resolved method, path, and decision-relevant input.
func TracePlatformCall(ctx core.Context, provider configpkg.CloudProviderConfig, method, path string, details ...string) {
	line := fmt.Sprintf("platform: %s %s%s", method, provider.ERun.APIURL, path)
	if len(details) > 0 {
		line += " (" + strings.Join(details, ", ") + ")"
	}
	ctx.Trace(line)
}
