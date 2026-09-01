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
// platform reads, exactly the way `erun platform` does: the explicit alias
// override when given, else the caller's sole configured erun alias. A
// tenant-level APIURL override, when configured (TenantConfig.APIURL — the
// same field `erun list` already treats as the tenant's own stable API
// address), still wins over the resolved alias's own ERun.APIURL; an
// environment's own loopback port-forward never does, and no longer
// participates in this resolution at all.
func (a *App) resolveTenantPlatform(tenant, aliasOverride string) (tenantPlatformResolution, error) {
	provider, err := eruncommon.ResolveERunPlatformAlias(a.deps.store, aliasOverride)
	if err != nil {
		return a.tenantPlatformResolutionForResolveError(err)
	}
	if provider.ERun == nil || strings.TrimSpace(provider.ERun.APIURL) == "" {
		// Mirrors newPlatformClientForAlias's own guard: a Provider already
		// CloudProviderERun with a nil/empty ERun block is incomplete, not
		// unconnected — a config-write defect the operator resolves by
		// re-running `erun cloud login`, not by reconnecting from scratch.
		return tenantPlatformResolution{}, fmt.Errorf("erun platform alias %q is incomplete (its erun api configuration is missing); run `erun cloud login %s` to restore it", provider.Alias, provider.Alias)
	}

	apiURL := provider.ERun.APIURL
	tenantConfig, _, err := a.deps.store.LoadTenantConfig(tenant)
	if err != nil && !errors.Is(err, eruncommon.ErrNotInitialized) {
		return tenantPlatformResolution{}, err
	}
	if override := strings.TrimSpace(tenantConfig.APIURL); override != "" {
		apiURL = override
	}

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
