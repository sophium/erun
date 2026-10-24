package eruncommon

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// isPlatformAliasAmbiguity reports whether err is that ambiguity, through any
// markPlatformAliasUnusable the resolver applied on the way out.
func isPlatformAliasAmbiguity(err error) bool {
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

// RunPlatformWhoami resolves the caller's identity against the erun platform.
func RunPlatformWhoami(ctx core.Context, store configpkg.CloudReadStore, alias string, deps cloud.CloudDependencies) (platformclient.PlatformWhoami, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return platformclient.PlatformWhoami{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "GET", "/v1/whoami")
	if ctx.DryRun {
		return platformclient.PlatformWhoami{}, nil
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
func RunPlatformVersion(ctx core.Context, store configpkg.CloudReadStore, alias string, deps cloud.CloudDependencies) (platformclient.PlatformInfo, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return platformclient.PlatformInfo{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "GET", "/v1/platform")
	if ctx.DryRun {
		return platformclient.PlatformInfo{}, nil
	}
	return client.Platform(context.Background())
}

// RunPlatformCreateTenant registers a new tenant. Requires an
// operations-tenant caller.
func RunPlatformCreateTenant(ctx core.Context, store configpkg.CloudReadStore, alias string, params platformclient.PlatformCreateTenantParams, deps cloud.CloudDependencies) (platformclient.PlatformTenant, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return platformclient.PlatformTenant{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "POST", "/v1/tenants",
		"name="+params.Name, "type="+params.Type, "issuer="+params.Issuer)
	if ctx.DryRun {
		return platformclient.PlatformTenant{}, nil
	}
	return client.CreateTenant(context.Background(), params)
}

// RunPlatformCreateOrg creates an organization on the platform's own IdP —
// the org an org-scoped tenant mapping needs before CreateTenant can produce
// one any token will ever resolve to. Requires an operations-tenant caller.
func RunPlatformCreateOrg(ctx core.Context, store configpkg.CloudReadStore, alias string, params platformclient.PlatformCreateOrgParams, deps cloud.CloudDependencies) (platformclient.PlatformOrg, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return platformclient.PlatformOrg{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "POST", "/v1/identity/orgs", "name="+params.Name)
	if ctx.DryRun {
		return platformclient.PlatformOrg{}, nil
	}
	return client.CreateOrg(context.Background(), params)
}

// RunPlatformRepairTenantIssuerOrgMapping fixes a tenant already stuck with
// an unresolvable (issuer, org) mapping -- the repair path for a tenant a
// pre-fix `platform tenant create` produced with no org value, or one whose
// issuer was converted to org-scoped after it registered. Requires an
// operations-tenant caller.
func RunPlatformRepairTenantIssuerOrgMapping(ctx core.Context, store configpkg.CloudReadStore, alias string, params platformclient.PlatformRepairTenantIssuerOrgMappingParams, deps cloud.CloudDependencies) (platformclient.PlatformTenantIssuer, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return platformclient.PlatformTenantIssuer{}, err
	}
	details := []string{"issuer=" + params.Issuer, "orgFieldKey=" + params.OrgFieldKey, "orgFieldValue=" + params.OrgFieldValue}
	if strings.TrimSpace(params.TenantID) != "" {
		details = append(details, "tenantId="+params.TenantID)
	}
	cloud.TracePlatformCall(ctx, provider, "PATCH", "/v1/tenant-issuers", details...)
	if ctx.DryRun {
		return platformclient.PlatformTenantIssuer{}, nil
	}
	return client.RepairTenantIssuerOrgMapping(context.Background(), params)
}

// RunPlatformListTenants lists tenants visible to the caller.
func RunPlatformListTenants(ctx core.Context, store configpkg.CloudReadStore, alias string, deps cloud.CloudDependencies) ([]platformclient.PlatformTenant, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return nil, err
	}
	cloud.TracePlatformCall(ctx, provider, "GET", "/v1/tenants")
	if ctx.DryRun {
		return nil, nil
	}
	return client.ListTenants(context.Background())
}

// RunPlatformCreateUser enrolls a user.
func RunPlatformCreateUser(ctx core.Context, store configpkg.CloudReadStore, alias string, params platformclient.PlatformCreateUserParams, deps cloud.CloudDependencies) (platformclient.PlatformUser, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return platformclient.PlatformUser{}, err
	}
	details := []string{"username=" + params.Username}
	if len(params.RoleIDs) > 0 {
		details = append(details, "roleIds="+strings.Join(params.RoleIDs, ","))
	}
	cloud.TracePlatformCall(ctx, provider, "POST", "/v1/users", details...)
	if ctx.DryRun {
		return platformclient.PlatformUser{}, nil
	}
	return client.CreateUser(context.Background(), params)
}

// RunPlatformListRoles lists the caller's tenant's roles and their
// permissions, the lookup a client needs to turn a missing capability into the
// role id a grant command takes.
func RunPlatformListRoles(ctx core.Context, store configpkg.CloudReadStore, alias string, deps cloud.CloudDependencies) ([]platformclient.PlatformRole, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return nil, err
	}
	cloud.TracePlatformCall(ctx, provider, "GET", "/v1/roles")
	if ctx.DryRun {
		return nil, nil
	}
	return client.ListRoles(context.Background())
}

// RunPlatformGrantUserRole grants one role to one already-enrolled user, the
// post-enrollment grant `erun platform user enroll` cannot perform.
func RunPlatformGrantUserRole(ctx core.Context, store configpkg.CloudReadStore, alias string, params platformclient.PlatformGrantUserRoleParams, deps cloud.CloudDependencies) error {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return err
	}
	path := "/v1/users/" + url.PathEscape(params.UserID) + "/roles"
	cloud.TracePlatformCall(ctx, provider, "POST", path, "roleId="+params.RoleID)
	if ctx.DryRun {
		return nil
	}
	return client.GrantUserRole(context.Background(), params)
}

// RunPlatformListUsers lists the target tenant's users.
func RunPlatformListUsers(ctx core.Context, store configpkg.CloudReadStore, alias string, params platformclient.PlatformListUsersParams, deps cloud.CloudDependencies) ([]platformclient.PlatformUser, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return nil, err
	}
	path := "/v1/users"
	if strings.TrimSpace(params.TenantID) != "" {
		path += "?tenantId=" + params.TenantID
	}
	cloud.TracePlatformCall(ctx, provider, "GET", path)
	if ctx.DryRun {
		return nil, nil
	}
	return client.ListUsers(context.Background(), params)
}

// RunPlatformListEnvironments lists the caller's tenant's environments.
func RunPlatformListEnvironments(ctx core.Context, store configpkg.CloudReadStore, alias string, deps cloud.CloudDependencies) ([]platformclient.PlatformEnvironment, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return nil, err
	}
	cloud.TracePlatformCall(ctx, provider, "GET", "/v1/environments")
	if ctx.DryRun {
		return nil, nil
	}
	return client.ListEnvironments(context.Background())
}

// RunPlatformGetEnvironment fetches one environment by id.
func RunPlatformGetEnvironment(ctx core.Context, store configpkg.CloudReadStore, alias, environmentID string, deps cloud.CloudDependencies) (platformclient.PlatformEnvironment, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return platformclient.PlatformEnvironment{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "GET", "/v1/environments/"+environmentID)
	if ctx.DryRun {
		return platformclient.PlatformEnvironment{}, nil
	}
	return client.GetEnvironment(context.Background(), environmentID)
}

// RunPlatformRegisterEnvironment registers an environment, optionally
// starting a server-side deploy (see PlatformClient.CreateEnvironment).
func RunPlatformRegisterEnvironment(ctx core.Context, store configpkg.CloudReadStore, alias string, params platformclient.PlatformCreateEnvironmentParams, deps cloud.CloudDependencies) (platformclient.PlatformEnvironment, error) {
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
func RunPlatformRegisterEnvironmentWithDefinition(ctx core.Context, store configpkg.CloudReadStore, alias string, params platformclient.PlatformCreateEnvironmentParams, local *EnvDefinitionAdoptParams, deps cloud.CloudDependencies) (platformclient.PlatformEnvironment, EnvDefinitionPushResult, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return platformclient.PlatformEnvironment{}, EnvDefinitionPushResult{}, err
	}
	if err := checkRegistrationDefinitionInput(params, local); err != nil {
		return platformclient.PlatformEnvironment{}, EnvDefinitionPushResult{}, err
	}
	tracePlatformRegistration(ctx, provider, params)
	if local != nil {
		ctx.Trace(fmt.Sprintf("definition: %s/%s is hosted at %s definition revision %d", local.Tenant, local.Environment, provider.ERun.APIURL, 1))
		cloud.TracePlatformCall(ctx, provider, "PUT", "/v1/environments/<adopted>/definition",
			"revision=1", "fields="+strings.Join(populatedPortableFieldsForLocal(*local), ","))
	}
	if ctx.DryRun {
		return platformclient.PlatformEnvironment{}, EnvDefinitionPushResult{}, nil
	}

	environment, err := client.CreateEnvironment(context.Background(), params)
	if err != nil {
		return platformclient.PlatformEnvironment{}, EnvDefinitionPushResult{}, err
	}
	if local == nil {
		return environment, EnvDefinitionPushResult{}, nil
	}
	result, err := AdoptEnvironmentDefinition(context.Background(), configpkg.ConfigStore{}, client, environment, *local)
	if err != nil {
		return environment, EnvDefinitionPushResult{}, err
	}
	return environment, result, nil
}

// checkAdoptInput mirrors the platform's own adopt contract before the request
// is sent, so a caller that omitted the context gets the reason rather than a
// 400 it has to interpret. The platform remains the authority; this is the
// same early refusal the desktop's registration flow performs.
func checkAdoptInput(params platformclient.PlatformCreateEnvironmentParams) error {
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
func checkRegistrationDefinitionInput(params platformclient.PlatformCreateEnvironmentParams, local *EnvDefinitionAdoptParams) error {
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
func tracePlatformRegistration(ctx core.Context, provider configpkg.CloudProviderConfig, params platformclient.PlatformCreateEnvironmentParams) {
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
	cloud.TracePlatformCall(ctx, provider, "POST", "/v1/environments", details...)
}

// populatedPortableFieldsForLocal reports which portable fields a named local
// environment would upload, for the dry-run trace. A local environment that
// cannot be read reports nothing rather than failing the whole dry run: the
// trace is a plan, and the real run is where a missing config is a refusal.
func populatedPortableFieldsForLocal(local EnvDefinitionAdoptParams) []string {
	config, _, err := configpkg.ConfigStore{}.LoadEnvConfig(local.Tenant, local.Environment)
	if err != nil {
		return nil
	}
	return configpkg.PopulatedPortableEnvConfigFields(config)
}

// RunPlatformPushEnvDefinition uploads a local environment's portable settings
// to the platform row its hosted marker names.
//
// Under --dry-run it traces the resolved local environment, the row the marker
// names, and the portable fields that would travel — never the fields that
// would not, so the trace reads as the allowlist itself rather than as an
// inventory of the config.
func RunPlatformPushEnvDefinition(ctx core.Context, store configpkg.CloudReadStore, alias string, params EnvDefinitionPushParams, deps cloud.CloudDependencies) (EnvDefinitionPushResult, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return EnvDefinitionPushResult{}, err
	}
	config, _, err := configpkg.ConfigStore{}.LoadEnvConfig(params.Tenant, params.Environment)
	if err != nil {
		return EnvDefinitionPushResult{}, fmt.Errorf("load %s/%s: %w", params.Tenant, params.Environment, err)
	}
	marker, ok := configpkg.HostedEnvironmentFromConfig(config)
	if !ok {
		return EnvDefinitionPushResult{}, notHostedError(params.Tenant, params.Environment)
	}
	ctx.Trace(fmt.Sprintf("definition: %s/%s is hosted at %s", params.Tenant, params.Environment, marker.Describe()))

	portable := configpkg.PopulatedPortableEnvConfigFields(config)
	cloud.TracePlatformCall(ctx, provider, "GET", "/v1/environments/"+marker.EnvironmentID)
	cloud.TracePlatformCall(ctx, provider, "PUT", "/v1/environments/"+marker.EnvironmentID+"/definition",
		"revision="+fmt.Sprintf("%d", marker.DefinitionRevision+1),
		"fields="+strings.Join(portable, ","))
	if ctx.DryRun {
		return EnvDefinitionPushResult{}, nil
	}
	return PushEnvironmentDefinition(context.Background(), configpkg.ConfigStore{}, client, params)
}

// RunPlatformPullEnvDefinition pulls the platform's stored definition into a
// local environment, creating it first when it does not exist yet.
//
// The confirmation rides in through confirm: a two-sided portable edit is a
// question for the operator, and a transport with no way to ask (MCP, a
// non-interactive run) passes nil and gets a refusal carrying the diff instead
// of a silent overwrite.
func RunPlatformPullEnvDefinition(ctx core.Context, store configpkg.CloudReadStore, alias string, params EnvDefinitionPullParams, confirm func(configpkg.EnvDefinitionPlan) (bool, error), deps cloud.CloudDependencies) (EnvDefinitionPullResult, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return EnvDefinitionPullResult{}, err
	}
	config, _, loadErr := configpkg.ConfigStore{}.LoadEnvConfig(params.Tenant, params.Environment)
	creating := errors.Is(loadErr, configpkg.ErrNotInitialized)
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
		cloud.TracePlatformCall(ctx, provider, "GET", "/v1/environments/"+environmentID)
	} else {
		marker, ok := configpkg.HostedEnvironmentFromConfig(config)
		if !ok {
			return EnvDefinitionPullResult{}, notHostedError(params.Tenant, params.Environment)
		}
		environmentID = marker.EnvironmentID
		ctx.Trace(fmt.Sprintf("definition: %s/%s is hosted at %s", params.Tenant, params.Environment, marker.Describe()))
		cloud.TracePlatformCall(ctx, provider, "GET", "/v1/environments/"+environmentID)
	}
	cloud.TracePlatformCall(ctx, provider, "GET", "/v1/environments/"+environmentID+"/definition")
	ctx.Trace("definition: only portable fields are written; host-owned and platform-owned fields are left alone")

	if ctx.DryRun {
		return EnvDefinitionPullResult{}, nil
	}
	return PullEnvironmentDefinition(context.Background(), configpkg.ConfigStore{}, client, params)
}

// RunPlatformDeployEnvironment starts a server-side deploy of an
// already-registered runtime environment.
func RunPlatformDeployEnvironment(ctx core.Context, store configpkg.CloudReadStore, alias, environmentID string, params platformclient.PlatformDeployEnvironmentParams, deps cloud.CloudDependencies) (platformclient.PlatformEnvironment, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return platformclient.PlatformEnvironment{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "POST", "/v1/environments/"+environmentID+"/deploy", "version="+params.Version)
	if ctx.DryRun {
		return platformclient.PlatformEnvironment{}, nil
	}
	return client.DeployEnvironment(context.Background(), environmentID, params)
}

// RunPlatformStopEnvironment scales a runtime environment's Deployment to
// zero, the server-side equivalent of `erun stop`.
func RunPlatformStopEnvironment(ctx core.Context, store configpkg.CloudReadStore, alias, environmentID string, deps cloud.CloudDependencies) (platformclient.PlatformEnvironment, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return platformclient.PlatformEnvironment{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "POST", "/v1/environments/"+environmentID+"/stop")
	if ctx.DryRun {
		return platformclient.PlatformEnvironment{}, nil
	}
	return client.StopEnvironment(context.Background(), environmentID)
}

// RunPlatformDeleteEnvironment starts tearing down a runtime environment's
// namespace and its row, the server-side equivalent of `erun delete`. See
// PlatformClient.DeleteEnvironment: the teardown itself runs asynchronously
// (#1140), so the returned environment reflects the claim (status
// "deleting"), not a completed removal.
func RunPlatformDeleteEnvironment(ctx core.Context, store configpkg.CloudReadStore, alias, environmentID string, deps cloud.CloudDependencies) (platformclient.PlatformEnvironment, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return platformclient.PlatformEnvironment{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "DELETE", "/v1/environments/"+environmentID)
	if ctx.DryRun {
		return platformclient.PlatformEnvironment{}, nil
	}
	return client.DeleteEnvironment(context.Background(), environmentID)
}

// RunPlatformListContexts lists the caller's tenant's cloud contexts.
func RunPlatformListContexts(ctx core.Context, store configpkg.CloudReadStore, alias string, deps cloud.CloudDependencies) ([]platformclient.PlatformContext, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return nil, err
	}
	cloud.TracePlatformCall(ctx, provider, "GET", "/v1/contexts")
	if ctx.DryRun {
		return nil, nil
	}
	return client.ListContexts(context.Background())
}

// RunPlatformGetContext fetches one cloud context by id.
func RunPlatformGetContext(ctx core.Context, store configpkg.CloudReadStore, alias, contextID string, deps cloud.CloudDependencies) (platformclient.PlatformContext, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return platformclient.PlatformContext{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "GET", "/v1/contexts/"+contextID)
	if ctx.DryRun {
		return platformclient.PlatformContext{}, nil
	}
	return client.GetContext(context.Background(), contextID)
}

// RunPlatformCreateContext registers a cloud context, or — with
// params.Preview set — only resolves and returns its bootstrap plan. Preview
// is a server-side dry run (it still reaches the platform); ctx.DryRun is the
// CLI/MCP-side one and skips the network call entirely.
func RunPlatformCreateContext(ctx core.Context, store configpkg.CloudReadStore, alias string, params platformclient.PlatformCreateContextParams, deps cloud.CloudDependencies) (platformclient.PlatformCreateContextResult, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return platformclient.PlatformCreateContextResult{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "POST", "/v1/contexts",
		"name="+params.Name, "alias="+params.CloudProviderAlias, "region="+params.Region, fmt.Sprintf("preview=%t", params.Preview))
	if ctx.DryRun {
		return platformclient.PlatformCreateContextResult{}, nil
	}
	return client.CreateContext(context.Background(), params)
}

// RunPlatformProvision resolves and returns the full ordered plan for
// provisioning a hosted environment, without executing any of it.
func RunPlatformProvision(ctx core.Context, store configpkg.CloudReadStore, alias string, params platformclient.PlatformProvisionParams, deps cloud.CloudDependencies) (platformclient.PlatformProvisionResult, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return platformclient.PlatformProvisionResult{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "POST", "/v1/provision", "env="+params.Environment.Name, "type="+params.Environment.Type)
	if ctx.DryRun {
		return platformclient.PlatformProvisionResult{}, nil
	}
	return client.Provision(context.Background(), params)
}
