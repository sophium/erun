package eruncommon

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// ResolveERunPlatformAlias resolves which "erun"-type cloud provider alias a
// platform command targets: the explicit alias when given (verified to be an
// erun-type alias), or the caller's sole configured erun alias when exactly
// one exists. Local config lookup only — never touches the network, so it is
// always safe to call in --dry-run mode.
func ResolveERunPlatformAlias(store CloudReadStore, alias string) (CloudProviderConfig, error) {
	alias = strings.TrimSpace(alias)
	if alias != "" {
		provider, err := ResolveCloudProvider(store, alias)
		if err != nil {
			return CloudProviderConfig{}, markPlatformAliasUnusable(err)
		}
		if provider.Provider != CloudProviderERun {
			return CloudProviderConfig{}, markPlatformAliasUnusable(fmt.Errorf("cloud provider alias %q is a %q-type alias, not an erun platform alias", provider.Alias, provider.Provider))
		}
		return provider, nil
	}
	providers, err := ListCloudProviders(store)
	if err != nil {
		return CloudProviderConfig{}, markPlatformAliasUnusable(err)
	}
	erunProviders := make([]CloudProviderConfig, 0, len(providers))
	for _, provider := range providers {
		if provider.Provider == CloudProviderERun {
			erunProviders = append(erunProviders, provider)
		}
	}
	switch len(erunProviders) {
	case 0:
		return CloudProviderConfig{}, markPlatformAliasUnusable(fmt.Errorf("no erun platform cloud provider alias is configured; run `erun cloud init erun --api-url <url>` first"))
	case 1:
		return erunProviders[0], nil
	default:
		return CloudProviderConfig{}, markPlatformAliasUnusable(fmt.Errorf("multiple erun platform cloud provider aliases are configured; pass --erun-alias to choose one"))
	}
}

// newPlatformClientForAlias resolves the erun platform alias and builds a
// PlatformClient against it, minting a fresh bearer token per call via the
// alias's stored refresh/access token.
func newPlatformClientForAlias(ctx Context, store CloudReadStore, alias string, deps CloudDependencies) (*PlatformClient, CloudProviderConfig, error) {
	provider, err := ResolveERunPlatformAlias(store, alias)
	if err != nil {
		return nil, CloudProviderConfig{}, err
	}
	if provider.ERun == nil || strings.TrimSpace(provider.ERun.APIURL) == "" {
		// A nil/empty ERun block on an alias whose Provider is already
		// CloudProviderERun is not "never configured" — init always writes it
		// together with Provider — it is incomplete: most likely truncated by a
		// config.yaml write from a component that doesn't know this field exists
		// Point at re-login rather than `cloud init`, which would
		// read as "start over" and paper over the real defect.
		return nil, CloudProviderConfig{}, markPlatformAliasUnusable(fmt.Errorf("erun platform alias %q is incomplete (its erun api configuration is missing, likely dropped by a config write from an older erun component); run `erun cloud login %s` to restore it", provider.Alias, provider.Alias))
	}
	client := NewPlatformClient(provider.ERun.APIURL, func() (string, error) {
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

// tracePlatformCall is the single audit line every platform command traces
// before it would send its HTTP request, satisfying the dry-run contract:
// callers skip the real client.* call under ctx.DryRun once this has traced
// the resolved method, path, and decision-relevant input.
func tracePlatformCall(ctx Context, provider CloudProviderConfig, method, path string, details ...string) {
	line := fmt.Sprintf("platform: %s %s%s", method, provider.ERun.APIURL, path)
	if len(details) > 0 {
		line += " (" + strings.Join(details, ", ") + ")"
	}
	ctx.Trace(line)
}

// RunPlatformWhoami resolves the caller's identity against the erun platform.
func RunPlatformWhoami(ctx Context, store CloudReadStore, alias string, deps CloudDependencies) (PlatformWhoami, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformWhoami{}, err
	}
	tracePlatformCall(ctx, provider, "GET", "/v1/whoami")
	if ctx.DryRun {
		return PlatformWhoami{}, nil
	}
	return client.Whoami(context.Background())
}

// RunPlatformCreateTenant registers a new tenant. Requires an
// operations-tenant caller.
func RunPlatformCreateTenant(ctx Context, store CloudReadStore, alias string, params PlatformCreateTenantParams, deps CloudDependencies) (PlatformTenant, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformTenant{}, err
	}
	tracePlatformCall(ctx, provider, "POST", "/v1/tenants",
		"name="+params.Name, "type="+params.Type, "issuer="+params.Issuer)
	if ctx.DryRun {
		return PlatformTenant{}, nil
	}
	return client.CreateTenant(context.Background(), params)
}

// RunPlatformCreateOrg creates an organization on the platform's own IdP —
// the org an org-scoped tenant mapping needs before CreateTenant can produce
// one any token will ever resolve to. Requires an operations-tenant caller.
func RunPlatformCreateOrg(ctx Context, store CloudReadStore, alias string, params PlatformCreateOrgParams, deps CloudDependencies) (PlatformOrg, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformOrg{}, err
	}
	tracePlatformCall(ctx, provider, "POST", "/v1/identity/orgs", "name="+params.Name)
	if ctx.DryRun {
		return PlatformOrg{}, nil
	}
	return client.CreateOrg(context.Background(), params)
}

// RunPlatformRepairTenantIssuerOrgMapping fixes a tenant already stuck with
// an unresolvable (issuer, org) mapping -- the repair path for a tenant a
// pre-fix `platform tenant create` produced with no org value, or one whose
// issuer was converted to org-scoped after it registered. Requires an
// operations-tenant caller.
func RunPlatformRepairTenantIssuerOrgMapping(ctx Context, store CloudReadStore, alias string, params PlatformRepairTenantIssuerOrgMappingParams, deps CloudDependencies) (PlatformTenantIssuer, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformTenantIssuer{}, err
	}
	details := []string{"issuer=" + params.Issuer, "orgFieldKey=" + params.OrgFieldKey, "orgFieldValue=" + params.OrgFieldValue}
	if strings.TrimSpace(params.TenantID) != "" {
		details = append(details, "tenantId="+params.TenantID)
	}
	tracePlatformCall(ctx, provider, "PATCH", "/v1/tenant-issuers", details...)
	if ctx.DryRun {
		return PlatformTenantIssuer{}, nil
	}
	return client.RepairTenantIssuerOrgMapping(context.Background(), params)
}

// RunPlatformListTenants lists tenants visible to the caller.
func RunPlatformListTenants(ctx Context, store CloudReadStore, alias string, deps CloudDependencies) ([]PlatformTenant, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return nil, err
	}
	tracePlatformCall(ctx, provider, "GET", "/v1/tenants")
	if ctx.DryRun {
		return nil, nil
	}
	return client.ListTenants(context.Background())
}

// RunPlatformCreateUser enrolls a user.
func RunPlatformCreateUser(ctx Context, store CloudReadStore, alias string, params PlatformCreateUserParams, deps CloudDependencies) (PlatformUser, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformUser{}, err
	}
	details := []string{"username=" + params.Username}
	if len(params.RoleIDs) > 0 {
		details = append(details, "roleIds="+strings.Join(params.RoleIDs, ","))
	}
	tracePlatformCall(ctx, provider, "POST", "/v1/users", details...)
	if ctx.DryRun {
		return PlatformUser{}, nil
	}
	return client.CreateUser(context.Background(), params)
}

// RunPlatformListUsers lists the target tenant's users.
func RunPlatformListUsers(ctx Context, store CloudReadStore, alias string, params PlatformListUsersParams, deps CloudDependencies) ([]PlatformUser, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return nil, err
	}
	path := "/v1/users"
	if strings.TrimSpace(params.TenantID) != "" {
		path += "?tenantId=" + params.TenantID
	}
	tracePlatformCall(ctx, provider, "GET", path)
	if ctx.DryRun {
		return nil, nil
	}
	return client.ListUsers(context.Background(), params)
}

// RunPlatformListEnvironments lists the caller's tenant's environments.
func RunPlatformListEnvironments(ctx Context, store CloudReadStore, alias string, deps CloudDependencies) ([]PlatformEnvironment, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return nil, err
	}
	tracePlatformCall(ctx, provider, "GET", "/v1/environments")
	if ctx.DryRun {
		return nil, nil
	}
	return client.ListEnvironments(context.Background())
}

// RunPlatformGetEnvironment fetches one environment by id.
func RunPlatformGetEnvironment(ctx Context, store CloudReadStore, alias, environmentID string, deps CloudDependencies) (PlatformEnvironment, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformEnvironment{}, err
	}
	tracePlatformCall(ctx, provider, "GET", "/v1/environments/"+environmentID)
	if ctx.DryRun {
		return PlatformEnvironment{}, nil
	}
	return client.GetEnvironment(context.Background(), environmentID)
}

// RunPlatformRegisterEnvironment registers an environment, optionally
// starting a server-side deploy (see PlatformClient.CreateEnvironment).
func RunPlatformRegisterEnvironment(ctx Context, store CloudReadStore, alias string, params PlatformCreateEnvironmentParams, deps CloudDependencies) (PlatformEnvironment, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformEnvironment{}, err
	}
	tracePlatformCall(ctx, provider, "POST", "/v1/environments",
		"name="+params.Name, "type="+params.Type, "contextId="+params.ContextID, "runtimeVersion="+params.RuntimeVersion)
	if ctx.DryRun {
		return PlatformEnvironment{}, nil
	}
	return client.CreateEnvironment(context.Background(), params)
}

// RunPlatformDeployEnvironment starts a server-side deploy of an
// already-registered runtime environment.
func RunPlatformDeployEnvironment(ctx Context, store CloudReadStore, alias, environmentID string, params PlatformDeployEnvironmentParams, deps CloudDependencies) (PlatformEnvironment, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformEnvironment{}, err
	}
	tracePlatformCall(ctx, provider, "POST", "/v1/environments/"+environmentID+"/deploy", "version="+params.Version)
	if ctx.DryRun {
		return PlatformEnvironment{}, nil
	}
	return client.DeployEnvironment(context.Background(), environmentID, params)
}

// RunPlatformStopEnvironment scales a runtime environment's Deployment to
// zero, the server-side equivalent of `erun stop`.
func RunPlatformStopEnvironment(ctx Context, store CloudReadStore, alias, environmentID string, deps CloudDependencies) (PlatformEnvironment, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformEnvironment{}, err
	}
	tracePlatformCall(ctx, provider, "POST", "/v1/environments/"+environmentID+"/stop")
	if ctx.DryRun {
		return PlatformEnvironment{}, nil
	}
	return client.StopEnvironment(context.Background(), environmentID)
}

// RunPlatformDeleteEnvironment starts tearing down a runtime environment's
// namespace and its row, the server-side equivalent of `erun delete`. See
// PlatformClient.DeleteEnvironment: the teardown itself runs asynchronously
// (#1140), so the returned environment reflects the claim (status
// "deleting"), not a completed removal.
func RunPlatformDeleteEnvironment(ctx Context, store CloudReadStore, alias, environmentID string, deps CloudDependencies) (PlatformEnvironment, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformEnvironment{}, err
	}
	tracePlatformCall(ctx, provider, "DELETE", "/v1/environments/"+environmentID)
	if ctx.DryRun {
		return PlatformEnvironment{}, nil
	}
	return client.DeleteEnvironment(context.Background(), environmentID)
}

// RunPlatformListContexts lists the caller's tenant's cloud contexts.
func RunPlatformListContexts(ctx Context, store CloudReadStore, alias string, deps CloudDependencies) ([]PlatformContext, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return nil, err
	}
	tracePlatformCall(ctx, provider, "GET", "/v1/contexts")
	if ctx.DryRun {
		return nil, nil
	}
	return client.ListContexts(context.Background())
}

// RunPlatformGetContext fetches one cloud context by id.
func RunPlatformGetContext(ctx Context, store CloudReadStore, alias, contextID string, deps CloudDependencies) (PlatformContext, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformContext{}, err
	}
	tracePlatformCall(ctx, provider, "GET", "/v1/contexts/"+contextID)
	if ctx.DryRun {
		return PlatformContext{}, nil
	}
	return client.GetContext(context.Background(), contextID)
}

// RunPlatformCreateContext registers a cloud context, or — with
// params.Preview set — only resolves and returns its bootstrap plan. Preview
// is a server-side dry run (it still reaches the platform); ctx.DryRun is the
// CLI/MCP-side one and skips the network call entirely.
func RunPlatformCreateContext(ctx Context, store CloudReadStore, alias string, params PlatformCreateContextParams, deps CloudDependencies) (PlatformCreateContextResult, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformCreateContextResult{}, err
	}
	tracePlatformCall(ctx, provider, "POST", "/v1/contexts",
		"name="+params.Name, "alias="+params.CloudProviderAlias, "region="+params.Region, fmt.Sprintf("preview=%t", params.Preview))
	if ctx.DryRun {
		return PlatformCreateContextResult{}, nil
	}
	return client.CreateContext(context.Background(), params)
}

// RunPlatformProvision resolves and returns the full ordered plan for
// provisioning a hosted environment, without executing any of it.
func RunPlatformProvision(ctx Context, store CloudReadStore, alias string, params PlatformProvisionParams, deps CloudDependencies) (PlatformProvisionResult, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformProvisionResult{}, err
	}
	tracePlatformCall(ctx, provider, "POST", "/v1/provision", "env="+params.Environment.Name, "type="+params.Environment.Type)
	if ctx.DryRun {
		return PlatformProvisionResult{}, nil
	}
	return client.Provision(context.Background(), params)
}
