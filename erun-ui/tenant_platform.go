package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// tenant_platform.go resolves which erun-hosted-platform identity backs the
// tenant dashboard's reads and writes, the same way `erun platform` does
// (erun-common's ResolveERunPlatformAlias / newPlatformClientForAlias)
// instead of the desktop inventing its own scheme from an environment's own
// apiUrl and the tenant's primary cloud alias — which may be any provider
// type and, for a loopback port-forward, is never the platform.

// Platform-readiness states surfaced to the frontend when the platform
// identity is not ready far enough to load the dashboard. Each maps to
// exactly one next action; tenantPlatformStateReady means the platform
// resolved and the caller may proceed to the identity read.
const (
	tenantPlatformStateReady        = ""
	tenantPlatformStateNotConnected = "not-connected"
	tenantPlatformStateChooseAlias  = "choose-alias"
	tenantPlatformStateNotSignedIn  = "not-signed-in"
	tenantPlatformStateNotEnrolled  = "not-enrolled"
	tenantPlatformStateNoPermission = "no-permission"
)

// tenantPlatformResolution is what resolveTenantPlatform found: either a
// ready-to-use client, or a state the caller cannot proceed past, plus enough
// context (resolved alias, URL, choices, identity claims) to render it.
type tenantPlatformResolution struct {
	state        string
	client       *eruncommon.PlatformClient
	apiURL       string
	alias        string
	aliasChoices []string
	issuer       string
	subject      string
}

// resolveTenantPlatform resolves the erun-type cloud alias backing tenant's
// platform reads. Once a tenant has attached any cloud alias at all, only an
// erun-type alias within that tenant's own selection may back its platform
// identity (resolveTenantERunPlatformAlias) — never a global alias the
// tenant itself did not attach, even when it is the only erun alias
// configured on the machine. Before this (erun#1955), a tenant whose
// selection was AWS-only still authenticated its platform reads with a
// different tenant's erun credential, because the resolution never looked at
// the tenant's own selection at all.
//
// The erun platform's own API URL always comes from the resolved alias
// (provider.ERun.APIURL), never from TenantConfig.APIURL: that field is
// documented only as `erun open`'s own port-forward address
// (erun-docs/docs/reference/configuration.md), and letting it redirect an
// unrelated alias's bearer token to an arbitrary address is exactly the
// credential-disclosure shape erun#1955 also flagged — filling in that field
// used to send one platform's bearer to whatever address the field named.
func (a *App) resolveTenantPlatform(tenant, aliasOverride string) (tenantPlatformResolution, error) {
	tenantConfig, _, err := a.deps.store.LoadTenantConfig(tenant)
	if err != nil && !errors.Is(err, eruncommon.ErrNotInitialized) {
		return tenantPlatformResolution{}, err
	}

	provider, terminal, err := a.resolveTenantERunPlatformAlias(tenantConfig, strings.TrimSpace(aliasOverride))
	if err != nil {
		return tenantPlatformResolution{}, err
	}
	if terminal != nil {
		return *terminal, nil
	}
	if provider.ERun == nil || strings.TrimSpace(provider.ERun.APIURL) == "" {
		// Mirrors newPlatformClientForAlias's own guard: a Provider already
		// CloudProviderERun with a nil/empty ERun block is incomplete, not
		// unconnected — a config-write defect the operator resolves by
		// re-running `erun cloud login`, not by reconnecting from scratch.
		return tenantPlatformResolution{}, fmt.Errorf("erun platform alias %q is incomplete (its erun api configuration is missing); run `erun cloud login %s` to restore it", provider.Alias, provider.Alias)
	}
	apiURL := provider.ERun.APIURL

	token, err := eruncommon.CloudProviderBearerToken(eruncommon.Context{}, a.deps.store, eruncommon.CloudBearerParams{Alias: provider.Alias}, a.deps.cloudDeps)
	bearer := strings.TrimSpace(token.Token)
	if err != nil || bearer == "" {
		return tenantPlatformResolution{state: tenantPlatformStateNotSignedIn, alias: provider.Alias, apiURL: apiURL}, nil
	}

	issuer, _ := eruncommon.IssuerFromUnverifiedJWT(bearer)
	subject, _ := eruncommon.SubjectFromUnverifiedJWT(bearer)
	client := eruncommon.NewPlatformClient(apiURL, func() (string, error) { return bearer, nil }).
		WithUsernameHint(a.tenantDashboardUsernameHint(provider.Alias))
	return tenantPlatformResolution{
		state:   tenantPlatformStateReady,
		client:  client,
		apiURL:  apiURL,
		alias:   provider.Alias,
		issuer:  issuer,
		subject: subject,
	}, nil
}

// resolveTenantERunPlatformAlias picks which erun-type cloud alias backs
// tenant's platform reads. It returns exactly one of: a usable provider (the
// caller proceeds to mint a bearer from it), a terminal resolution the
// caller returns as-is (not-connected / choose-alias), or an error.
//
// An explicit aliasOverride — the operator's own choose-alias pick, or an
// MCP/dashboard caller naming one directly — is always honored verbatim,
// exactly like `erun platform --erun-alias`; it is a deliberate one-time
// choice, not an implicit default, so it is not further restricted to the
// tenant's own selection.
//
// Otherwise: a tenant that has never attached any cloud alias at all
// (TenantConfig.CloudProviderAliases empty) keeps the prior convenience of
// falling back to the machine's sole configured erun alias, since the
// tenant has expressed no preference to consult. A tenant that HAS attached
// aliases is scoped strictly to the erun-type aliases within that selection
// (erun-docs/docs/reference/configuration.md: "cloud provider aliases the
// tenant is allowed to use") — none found there means not-connected, full
// stop, never a fallback to an alias the tenant did not attach.
func (a *App) resolveTenantERunPlatformAlias(tenantConfig eruncommon.TenantConfig, aliasOverride string) (provider eruncommon.CloudProviderConfig, terminal *tenantPlatformResolution, err error) {
	if aliasOverride != "" || len(tenantConfig.CloudProviderAliases) == 0 {
		return a.resolveGlobalERunPlatformAlias(aliasOverride)
	}

	tenantERunAliases := a.tenantSelectedERunAliases(tenantConfig)
	if len(tenantERunAliases) == 0 {
		return eruncommon.CloudProviderConfig{}, &tenantPlatformResolution{state: tenantPlatformStateNotConnected}, nil
	}
	chosen := chooseTenantERunAlias(tenantERunAliases, tenantConfig.PrimaryCloudProviderAlias)
	if chosen == "" {
		return eruncommon.CloudProviderConfig{}, &tenantPlatformResolution{state: tenantPlatformStateChooseAlias, aliasChoices: tenantERunAliases}, nil
	}
	provider, err = eruncommon.ResolveCloudProvider(a.deps.store, chosen)
	if err != nil {
		return eruncommon.CloudProviderConfig{}, nil, err
	}
	return provider, nil, nil
}

// resolveGlobalERunPlatformAlias is the pre-erun#1955 resolution, unchanged:
// the explicit override when given, else the machine's sole configured erun
// alias. Used both for an explicit aliasOverride (always honored verbatim)
// and for a tenant that has never attached any cloud alias at all.
func (a *App) resolveGlobalERunPlatformAlias(aliasOverride string) (eruncommon.CloudProviderConfig, *tenantPlatformResolution, error) {
	provider, err := eruncommon.ResolveERunPlatformAlias(a.deps.store, aliasOverride)
	if err != nil {
		resolution, resolveErr := a.tenantPlatformResolutionForResolveError(err)
		return eruncommon.CloudProviderConfig{}, &resolution, resolveErr
	}
	return provider, nil, nil
}

// tenantSelectedERunAliases filters tenantConfig's own attached aliases down
// to the erun-type ones, preserving order. A stale/removed alias name in the
// tenant's own selection is skipped rather than failing the whole read.
func (a *App) tenantSelectedERunAliases(tenantConfig eruncommon.TenantConfig) []string {
	var erunAliases []string
	for _, alias := range tenantConfig.CloudProviderAliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		candidate, err := eruncommon.ResolveCloudProvider(a.deps.store, alias)
		if err != nil || candidate.Provider != eruncommon.CloudProviderERun {
			continue
		}
		erunAliases = append(erunAliases, alias)
	}
	return erunAliases
}

// chooseTenantERunAlias picks primary when it names one of candidates, the
// sole candidate when there is exactly one, or "" (ambiguous — the caller
// renders choose-alias) otherwise.
func chooseTenantERunAlias(candidates []string, primary string) string {
	primary = strings.TrimSpace(primary)
	for _, alias := range candidates {
		if alias == primary {
			return alias
		}
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	return ""
}

// tenantPlatformResolutionForResolveError classifies why
// ResolveERunPlatformAlias could not resolve a single alias: none configured
// at all (not-connected) versus more than one with no override (choose-
// alias, listing every alias so the frontend can offer them). Any other
// failure (an explicit override naming a non-erun or unknown alias) is a
// caller-wiring condition, not a state the frontend renders, so it surfaces
// as a real error.
func (a *App) tenantPlatformResolutionForResolveError(resolveErr error) (tenantPlatformResolution, error) {
	providers, err := eruncommon.ListCloudProviders(a.deps.store)
	if err != nil {
		return tenantPlatformResolution{}, err
	}
	var aliases []string
	for _, provider := range providers {
		if provider.Provider == eruncommon.CloudProviderERun {
			aliases = append(aliases, provider.Alias)
		}
	}
	switch len(aliases) {
	case 0:
		return tenantPlatformResolution{state: tenantPlatformStateNotConnected}, nil
	case 1:
		return tenantPlatformResolution{}, resolveErr
	default:
		return tenantPlatformResolution{state: tenantPlatformStateChooseAlias, aliasChoices: aliases}, nil
	}
}

// tenantPlatformClient resolves tenant's platform (resolveTenantPlatform) and
// returns a ready-to-use client plus a request-scoped context, for the review
// detail/write paths that run only after the dashboard's own platform
// resolution has already succeeded once (opening a review's detail or
// writing to it is only reachable from an already-loaded Reviews tab). A
// not-ready state therefore surfaces as a plain error here rather than the
// dashboard's own four-state UI.
func (a *App) tenantPlatformClient(ctx context.Context, tenant string) (*eruncommon.PlatformClient, context.Context, context.CancelFunc, error) {
	resolution, err := a.resolveTenantPlatform(tenant, "")
	if err != nil {
		return nil, nil, nil, err
	}
	if resolution.state != tenantPlatformStateReady {
		return nil, nil, nil, fmt.Errorf("tenant platform is not ready (%s); reopen the dashboard to resolve it", resolution.state)
	}
	requestCtx, cancel := context.WithTimeout(ctx, tenantDashboardTimeout)
	return resolution.client, requestCtx, cancel, nil
}

// uiConnectERunPlatformInput is the "Connect to erunpaas.com" action's input:
// just the API base URL, discovered against the platform itself the same way
// `erun cloud init erun` already does.
type uiConnectERunPlatformInput struct {
	APIURL string `json:"apiUrl"`
}

// ConnectERunPlatform attaches a hosted erun platform as a cloud alias, so the
// not-connected state has an in-app path with no terminal and no hand-typed
// URL beyond the base address itself.
func (a *App) ConnectERunPlatform(input uiConnectERunPlatformInput) (uiCloudProviderStatus, error) {
	provider, err := eruncommon.InitERunCloudProvider(eruncommon.Context{}, a.deps.store, eruncommon.InitERunCloudProviderParams{
		APIURL: strings.TrimSpace(input.APIURL),
	}, a.deps.cloudDeps)
	if err != nil {
		return uiCloudProviderStatus{}, err
	}
	return cloudProviderStatusToUI(eruncommon.CloudProviderTokenStatus(provider, a.deps.cloudDeps)), nil
}

// uiPlatformUserEnrollInput is the "not enrolled" state's enrollment attempt:
// every field prefilled from the identity already in hand
// (dashboard.platformIssuer/platformSubject), so the operator never retypes
// values erun already knows.
type uiPlatformUserEnrollInput struct {
	Alias    string `json:"alias"`
	Username string `json:"username"`
	Issuer   string `json:"issuer"`
	Subject  string `json:"subject"`
}

// uiPlatformUser is the enrolled row the platform returned.
type uiPlatformUser struct {
	UserID   string `json:"userId"`
	TenantID string `json:"tenantId"`
	Username string `json:"username"`
}

// EnrollERunPlatformUser attempts to enroll the given identity into the
// tenant. This succeeds only for a tenant with no users yet (first-user
// bootstrap) or when the caller already holds user-management capability on
// the platform; a not-yet-enrolled caller's own identity is refused by the
// platform's own auth middleware before this call is even authorized — the
// expected outcome for the common case, so the refusal is classified into
// the administrator hand-off (enrollERunPlatformUserError,
// tenant_platform_error.go) rather than surfaced as the raw platform error.
func (a *App) EnrollERunPlatformUser(input uiPlatformUserEnrollInput) (uiPlatformUser, error) {
	user, err := eruncommon.RunPlatformCreateUser(eruncommon.Context{}, a.deps.store, strings.TrimSpace(input.Alias), eruncommon.PlatformCreateUserParams{
		Username: strings.TrimSpace(input.Username),
		Issuer:   strings.TrimSpace(input.Issuer),
		Subject:  strings.TrimSpace(input.Subject),
	}, a.deps.cloudDeps)
	if err != nil {
		return uiPlatformUser{}, enrollERunPlatformUserError(err)
	}
	return uiPlatformUser{UserID: user.UserID, TenantID: user.TenantID, Username: user.Username}, nil
}
