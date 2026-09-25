package eruncommon

import (
	"context"
	"errors"
	"fmt"
	"net/url"
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

// platformAliasSelection names, in the vocabulary of the transport the
// operator is actually using, the argument that picks one alias out of
// several. The CLI and the MCP server resolve aliases through this same
// function, but their operators make that choice differently: a remedy that
// spells it the other transport's way is not something the reader can type.
type platformAliasSelection string

const (
	// platformAliasSelectionCLIFlag is the CLI's persistent --erun-alias flag.
	platformAliasSelectionCLIFlag platformAliasSelection = "--erun-alias"
	// platformAliasSelectionMCPParam is the MCP platform tools' `alias` input.
	platformAliasSelectionMCPParam platformAliasSelection = "alias"
)

// selectionArgument reports which spelling the calling transport's operator
// must use. An MCP tool call carries the tool name; the CLI and the desktop UI
// do not, and both select with the flag.
func platformAliasSelectionFor(ctx Context) platformAliasSelection {
	if strings.TrimSpace(ctx.MCPTool) != "" {
		return platformAliasSelectionMCPParam
	}
	return platformAliasSelectionCLIFlag
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

// ResolveERunPlatformAlias resolves which "erun"-type cloud provider alias a
// platform command targets: the explicit alias when given (verified to be an
// erun-type alias), or the caller's sole configured erun alias when exactly
// one exists. Local config lookup only — never touches the network, so it is
// always safe to call in --dry-run mode.
func ResolveERunPlatformAlias(store CloudReadStore, alias string) (CloudProviderConfig, error) {
	return resolveERunPlatformAlias(store, alias, platformAliasSelectionCLIFlag)
}

// resolveERunPlatformAlias is ResolveERunPlatformAlias with the selection
// argument spelled for the caller's own transport.
func resolveERunPlatformAlias(store CloudReadStore, alias string, selection platformAliasSelection) (CloudProviderConfig, error) {
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
		return CloudProviderConfig{}, markPlatformAliasUnusable(unresolvedPlatformAliasError(selection))
	case 1:
		return erunProviders[0], nil
	default:
		return CloudProviderConfig{}, markPlatformAliasUnusable(fmt.Errorf("multiple erun platform cloud provider aliases are configured; pass %s to choose one", selection))
	}
}

// newPlatformClientForAlias resolves the erun platform alias and builds a
// PlatformClient against it, minting a fresh bearer token per call via the
// alias's stored refresh/access token.
func newPlatformClientForAlias(ctx Context, store CloudReadStore, alias string, deps CloudDependencies) (*PlatformClient, CloudProviderConfig, error) {
	provider, err := resolveERunPlatformAlias(store, alias, platformAliasSelectionFor(ctx))
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

// RunPlatformVersion reports the build actually serving a hosted erun
// platform's own API, over GET /v1/platform — the same unauthenticated route
// `erun cloud init erun` already calls to discover the issuer and client ids.
// It answers erun#2052's gap directly: `whoami` proves the caller is
// authenticated and reachable, but returns identity, not build, so there was
// no way to ask a deployed control plane what version it is running.
// Unauthenticated on purpose: a client has to be able to compare the plane's
// version before it can even mint a token against it (the same reason
// PlatformInfo's other discovery fields -- issuer, client ids -- are
// unauthenticated), and the fields it returns are this instance's own
// self-description, never tenant data. This still goes through the resolved
// alias (not a bare --api-url) so the same call keeps working when the
// alias's cached credentials are stale or expired -- exactly the state a
// caller troubleshooting a 404/401 on some other route is likely to be in.
func RunPlatformVersion(ctx Context, store CloudReadStore, alias string, deps CloudDependencies) (PlatformInfo, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformInfo{}, err
	}
	tracePlatformCall(ctx, provider, "GET", "/v1/platform")
	if ctx.DryRun {
		return PlatformInfo{}, nil
	}
	return client.Platform(context.Background())
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

// RunPlatformListRoles lists the caller's tenant's roles and their
// permissions, the lookup a client needs to turn a missing capability into the
// role id a grant command takes.
func RunPlatformListRoles(ctx Context, store CloudReadStore, alias string, deps CloudDependencies) ([]PlatformRole, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return nil, err
	}
	tracePlatformCall(ctx, provider, "GET", "/v1/roles")
	if ctx.DryRun {
		return nil, nil
	}
	return client.ListRoles(context.Background())
}

// RunPlatformGrantUserRole grants one role to one already-enrolled user, the
// post-enrollment grant `erun platform user enroll` cannot perform.
func RunPlatformGrantUserRole(ctx Context, store CloudReadStore, alias string, params PlatformGrantUserRoleParams, deps CloudDependencies) error {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return err
	}
	path := "/v1/users/" + url.PathEscape(params.UserID) + "/roles"
	tracePlatformCall(ctx, provider, "POST", path, "roleId="+params.RoleID)
	if ctx.DryRun {
		return nil
	}
	return client.GrantUserRole(context.Background(), params)
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
	environment, _, err := RunPlatformRegisterEnvironmentWithDefinition(ctx, store, alias, params, nil, deps)
	return environment, err
}

// RunPlatformRegisterEnvironmentWithDefinition registers (or adopts) an
// environment and, when local names a local environment, uploads that
// environment's portable settings and records its hosted marker in the same
// step.
//
// The two are one call rather than two the caller sequences because recording
// the marker only after the upload succeeds is what makes the marker mean "the
// platform holds this revision": a marker written first would claim a sync that
// never happened.
func RunPlatformRegisterEnvironmentWithDefinition(ctx Context, store CloudReadStore, alias string, params PlatformCreateEnvironmentParams, local *EnvDefinitionAdoptParams, deps CloudDependencies) (PlatformEnvironment, EnvDefinitionPushResult, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformEnvironment{}, EnvDefinitionPushResult{}, err
	}
	if err := checkRegistrationDefinitionInput(params, local); err != nil {
		return PlatformEnvironment{}, EnvDefinitionPushResult{}, err
	}
	tracePlatformRegistration(ctx, provider, params)
	if local != nil {
		ctx.Trace(fmt.Sprintf("definition: %s/%s is hosted at %s definition revision %d", local.Tenant, local.Environment, provider.ERun.APIURL, 1))
		tracePlatformCall(ctx, provider, "PUT", "/v1/environments/<adopted>/definition",
			"revision=1", "fields="+strings.Join(populatedPortableFieldsForLocal(*local), ","))
	}
	if ctx.DryRun {
		return PlatformEnvironment{}, EnvDefinitionPushResult{}, nil
	}

	environment, err := client.CreateEnvironment(context.Background(), params)
	if err != nil {
		return PlatformEnvironment{}, EnvDefinitionPushResult{}, err
	}
	if local == nil {
		return environment, EnvDefinitionPushResult{}, nil
	}
	result, err := AdoptEnvironmentDefinition(context.Background(), ConfigStore{}, client, environment, *local)
	if err != nil {
		return environment, EnvDefinitionPushResult{}, err
	}
	return environment, result, nil
}

// checkAdoptInput mirrors the platform's own adopt contract before the request
// is sent, so a caller that omitted the context gets the reason rather than a
// 400 it has to interpret. The platform remains the authority; this is the
// same early refusal the desktop's registration flow performs.
func checkAdoptInput(params PlatformCreateEnvironmentParams) error {
	if !params.Adopt {
		return nil
	}
	if strings.TrimSpace(params.KubernetesContext) == "" {
		return fmt.Errorf("--adopt requires --kubernetes-context: adopting records the context the environment already runs against, and the platform refuses a runtime version or context id alongside it")
	}
	return nil
}

// checkRegistrationDefinitionInput runs the two client-side preconditions both
// halves of a registration share, before anything is sent: adopting records the
// context the environment already runs against, and uploading a definition is
// what records this machine as following the row.
func checkRegistrationDefinitionInput(params PlatformCreateEnvironmentParams, local *EnvDefinitionAdoptParams) error {
	if err := checkAdoptInput(params); err != nil {
		return err
	}
	if local != nil && !params.Adopt {
		return fmt.Errorf("--definition requires --adopt: uploading a local environment's settings records this machine as following that row, which is what adopting means")
	}
	return nil
}

// tracePlatformRegistration traces the registration call itself, shared by the
// plain and definition-carrying paths so the two cannot drift in what they
// report. The adopt/tenant/context keys appear only when they carry a value, so
// an ordinary registration's trace is exactly what it has always been.
func tracePlatformRegistration(ctx Context, provider CloudProviderConfig, params PlatformCreateEnvironmentParams) {
	details := []string{
		"name=" + params.Name,
		"type=" + params.Type,
		"contextId=" + params.ContextID,
		"runtimeVersion=" + params.RuntimeVersion,
	}
	if params.Adopt {
		details = append(details, "adopt=true")
	}
	if strings.TrimSpace(params.KubernetesContext) != "" {
		details = append(details, "kubernetesContext="+params.KubernetesContext)
	}
	if strings.TrimSpace(params.TenantID) != "" {
		details = append(details, "tenantId="+params.TenantID)
	}
	tracePlatformCall(ctx, provider, "POST", "/v1/environments", details...)
}

// populatedPortableFieldsForLocal reports which portable fields a named local
// environment would upload, for the dry-run trace. A local environment that
// cannot be read reports nothing rather than failing the whole dry run: the
// trace is a plan, and the real run is where a missing config is a refusal.
func populatedPortableFieldsForLocal(local EnvDefinitionAdoptParams) []string {
	config, _, err := ConfigStore{}.LoadEnvConfig(local.Tenant, local.Environment)
	if err != nil {
		return nil
	}
	return PopulatedPortableEnvConfigFields(config)
}

// RunPlatformPushEnvDefinition uploads a local environment's portable settings
// to the platform row its hosted marker names.
//
// Under --dry-run it traces the resolved local environment, the row the marker
// names, and the portable fields that would travel — never the fields that
// would not, so the trace reads as the allowlist itself rather than as an
// inventory of the config.
func RunPlatformPushEnvDefinition(ctx Context, store CloudReadStore, alias string, params EnvDefinitionPushParams, deps CloudDependencies) (EnvDefinitionPushResult, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return EnvDefinitionPushResult{}, err
	}
	config, _, err := ConfigStore{}.LoadEnvConfig(params.Tenant, params.Environment)
	if err != nil {
		return EnvDefinitionPushResult{}, fmt.Errorf("load %s/%s: %w", params.Tenant, params.Environment, err)
	}
	marker, ok := HostedEnvironmentFromConfig(config)
	if !ok {
		return EnvDefinitionPushResult{}, notHostedError(params.Tenant, params.Environment)
	}
	ctx.Trace(fmt.Sprintf("definition: %s/%s is hosted at %s", params.Tenant, params.Environment, marker.Describe()))

	portable := PopulatedPortableEnvConfigFields(config)
	tracePlatformCall(ctx, provider, "GET", "/v1/environments/"+marker.EnvironmentID)
	tracePlatformCall(ctx, provider, "PUT", "/v1/environments/"+marker.EnvironmentID+"/definition",
		"revision="+fmt.Sprintf("%d", marker.DefinitionRevision+1),
		"fields="+strings.Join(portable, ","))
	if ctx.DryRun {
		return EnvDefinitionPushResult{}, nil
	}
	return PushEnvironmentDefinition(context.Background(), ConfigStore{}, client, params)
}

// RunPlatformPullEnvDefinition pulls the platform's stored definition into a
// local environment, creating it first when it does not exist yet.
//
// The confirmation rides in through confirm: a two-sided portable edit is a
// question for the operator, and a transport with no way to ask (MCP, a
// non-interactive run) passes nil and gets a refusal carrying the diff instead
// of a silent overwrite.
func RunPlatformPullEnvDefinition(ctx Context, store CloudReadStore, alias string, params EnvDefinitionPullParams, confirm func(EnvDefinitionPlan) (bool, error), deps CloudDependencies) (EnvDefinitionPullResult, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return EnvDefinitionPullResult{}, err
	}
	config, _, loadErr := ConfigStore{}.LoadEnvConfig(params.Tenant, params.Environment)
	creating := errors.Is(loadErr, ErrNotInitialized)
	if loadErr != nil && !creating {
		return EnvDefinitionPullResult{}, fmt.Errorf("load %s/%s: %w", params.Tenant, params.Environment, loadErr)
	}

	environmentID := strings.TrimSpace(params.EnvironmentID)
	if creating {
		if environmentID == "" {
			return EnvDefinitionPullResult{}, fmt.Errorf(
				"%s/%s does not exist on this machine: a pull into a new environment must name the platform row with --environment-id (find it with `erun platform env list`)",
				params.Tenant, params.Environment)
		}
		ctx.Trace(fmt.Sprintf("definition: creating local environment %s/%s from platform row %s", params.Tenant, params.Environment, environmentID))
		tracePlatformCall(ctx, provider, "GET", "/v1/environments/"+environmentID)
	} else {
		marker, ok := HostedEnvironmentFromConfig(config)
		if !ok {
			return EnvDefinitionPullResult{}, notHostedError(params.Tenant, params.Environment)
		}
		environmentID = marker.EnvironmentID
		ctx.Trace(fmt.Sprintf("definition: %s/%s is hosted at %s", params.Tenant, params.Environment, marker.Describe()))
		tracePlatformCall(ctx, provider, "GET", "/v1/environments/"+environmentID)
	}
	tracePlatformCall(ctx, provider, "GET", "/v1/environments/"+environmentID+"/definition")
	ctx.Trace("definition: only portable fields are written; host-owned and platform-owned fields are left alone")

	if ctx.DryRun {
		return EnvDefinitionPullResult{}, nil
	}
	return PullEnvironmentDefinition(context.Background(), ConfigStore{}, client, params)
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
