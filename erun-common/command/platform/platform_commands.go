package platform

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// RunPlatformWhoami resolves the caller's identity.
func RunPlatformWhoami(ctx core.Context, store configpkg.CloudReadStore, alias string, deps cloud.CloudDependencies) (model.PlatformWhoami, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformWhoami{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "GET", "/v1/whoami")
	if ctx.DryRun {
		return model.PlatformWhoami{}, nil
	}
	return client.Whoami(context.Background())
}

// RunPlatformVersion reports the build serving the platform API. It is
// unauthenticated so it works before a token can be minted or with stale credentials.
func RunPlatformVersion(ctx core.Context, store configpkg.CloudReadStore, alias string, deps cloud.CloudDependencies) (model.PlatformInfo, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformInfo{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "GET", "/v1/platform")
	if ctx.DryRun {
		return model.PlatformInfo{}, nil
	}
	return client.Platform(context.Background())
}

// RunPlatformCreateTenant registers a tenant; requires an operations-tenant caller.
func RunPlatformCreateTenant(ctx core.Context, store configpkg.CloudReadStore, alias string, params model.PlatformCreateTenantParams, deps cloud.CloudDependencies) (model.PlatformTenant, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformTenant{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "POST", "/v1/tenants",
		"name="+params.Name, "type="+params.Type, "issuer="+params.Issuer)
	if ctx.DryRun {
		return model.PlatformTenant{}, nil
	}
	return client.CreateTenant(context.Background(), params)
}

// RunPlatformCreateOrg creates an org on the platform IdP; requires an operations-tenant caller.
func RunPlatformCreateOrg(ctx core.Context, store configpkg.CloudReadStore, alias string, params model.PlatformCreateOrgParams, deps cloud.CloudDependencies) (model.PlatformOrg, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformOrg{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "POST", "/v1/identity/orgs", "name="+params.Name)
	if ctx.DryRun {
		return model.PlatformOrg{}, nil
	}
	return client.CreateOrg(context.Background(), params)
}

// RunPlatformRepairTenantIssuerOrgMapping fixes a tenant with an unresolvable (issuer, org)
// mapping; requires an operations-tenant caller.
func RunPlatformRepairTenantIssuerOrgMapping(ctx core.Context, store configpkg.CloudReadStore, alias string, params model.PlatformRepairTenantIssuerOrgMappingParams, deps cloud.CloudDependencies) (model.PlatformTenantIssuer, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformTenantIssuer{}, err
	}
	details := []string{"issuer=" + params.Issuer, "orgFieldKey=" + params.OrgFieldKey, "orgFieldValue=" + params.OrgFieldValue}
	if strings.TrimSpace(params.TenantID) != "" {
		details = append(details, "tenantId="+params.TenantID)
	}
	cloud.TracePlatformCall(ctx, provider, "PATCH", "/v1/tenant-issuers", details...)
	if ctx.DryRun {
		return model.PlatformTenantIssuer{}, nil
	}
	return client.RepairTenantIssuerOrgMapping(context.Background(), params)
}

// RunPlatformListTenants lists tenants visible to the caller.
func RunPlatformListTenants(ctx core.Context, store configpkg.CloudReadStore, alias string, deps cloud.CloudDependencies) ([]model.PlatformTenant, error) {
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
func RunPlatformCreateUser(ctx core.Context, store configpkg.CloudReadStore, alias string, params model.PlatformCreateUserParams, deps cloud.CloudDependencies) (model.PlatformUser, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformUser{}, err
	}
	details := []string{"username=" + params.Username}
	if len(params.RoleIDs) > 0 {
		details = append(details, "roleIds="+strings.Join(params.RoleIDs, ","))
	}
	cloud.TracePlatformCall(ctx, provider, "POST", "/v1/users", details...)
	if ctx.DryRun {
		return model.PlatformUser{}, nil
	}
	return client.CreateUser(context.Background(), params)
}

// RunPlatformListRoles lists the caller's tenant's roles and permissions.
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

// RunPlatformGrantUserRole grants one role to an enrolled user.
func RunPlatformGrantUserRole(ctx core.Context, store configpkg.CloudReadStore, alias string, params model.PlatformGrantUserRoleParams, deps cloud.CloudDependencies) error {
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
func RunPlatformListUsers(ctx core.Context, store configpkg.CloudReadStore, alias string, params model.PlatformListUsersParams, deps cloud.CloudDependencies) ([]model.PlatformUser, error) {
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
func RunPlatformListEnvironments(ctx core.Context, store configpkg.CloudReadStore, alias string, deps cloud.CloudDependencies) ([]model.PlatformEnvironment, error) {
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
func RunPlatformGetEnvironment(ctx core.Context, store configpkg.CloudReadStore, alias, environmentID string, deps cloud.CloudDependencies) (model.PlatformEnvironment, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformEnvironment{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "GET", "/v1/environments/"+environmentID)
	if ctx.DryRun {
		return model.PlatformEnvironment{}, nil
	}
	return client.GetEnvironment(context.Background(), environmentID)
}

// RunPlatformRegisterEnvironment registers an environment, optionally deploying it.
func RunPlatformRegisterEnvironment(ctx core.Context, store configpkg.CloudReadStore, alias string, params model.PlatformCreateEnvironmentParams, deps cloud.CloudDependencies) (model.PlatformEnvironment, error) {
	environment, _, err := RunPlatformRegisterEnvironmentWithDefinition(ctx, store, alias, params, nil, deps)
	return environment, err
}

// RunPlatformRegisterEnvironmentWithDefinition registers an environment and uploads a
// local definition. The marker is recorded only after the upload succeeds.
func RunPlatformRegisterEnvironmentWithDefinition(ctx core.Context, store configpkg.CloudReadStore, alias string, params model.PlatformCreateEnvironmentParams, local *model.EnvDefinitionAdoptParams, deps cloud.CloudDependencies) (model.PlatformEnvironment, EnvDefinitionPushResult, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformEnvironment{}, EnvDefinitionPushResult{}, err
	}
	if err := checkRegistrationDefinitionInput(params, local); err != nil {
		return model.PlatformEnvironment{}, EnvDefinitionPushResult{}, err
	}
	tracePlatformRegistration(ctx, provider, params)
	if local != nil {
		ctx.Trace(fmt.Sprintf("definition: %s/%s is hosted at %s definition revision %d", local.Tenant, local.Environment, provider.ERun.APIURL, 1))
		cloud.TracePlatformCall(ctx, provider, "PUT", "/v1/environments/<adopted>/definition",
			"revision=1", "fields="+strings.Join(populatedPortableFieldsForLocal(*local), ","))
	}
	if ctx.DryRun {
		return model.PlatformEnvironment{}, EnvDefinitionPushResult{}, nil
	}

	environment, err := client.CreateEnvironment(context.Background(), params)
	if err != nil {
		return model.PlatformEnvironment{}, EnvDefinitionPushResult{}, err
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

// checkAdoptInput mirrors the platform's adopt contract so the caller gets a reason, not a 400.
func checkAdoptInput(params model.PlatformCreateEnvironmentParams) error {
	if !params.Adopt {
		return nil
	}
	if strings.TrimSpace(params.KubernetesContext) == "" {
		return fmt.Errorf("--adopt requires --kubernetes-context: adopting records the context the environment already runs against, and the platform refuses a runtime version or context id alongside it")
	}
	return nil
}

func checkRegistrationDefinitionInput(params model.PlatformCreateEnvironmentParams, local *model.EnvDefinitionAdoptParams) error {
	if err := checkAdoptInput(params); err != nil {
		return err
	}
	if local != nil && !params.Adopt {
		return fmt.Errorf("--definition requires --adopt: uploading a local environment's settings records this machine as following that row, which is what adopting means")
	}
	return nil
}

func tracePlatformRegistration(ctx core.Context, provider model.CloudProviderConfig, params model.PlatformCreateEnvironmentParams) {
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

// populatedPortableFieldsForLocal reports nothing for an unreadable local environment:
// the dry run is a plan, the real run refuses.
func populatedPortableFieldsForLocal(local model.EnvDefinitionAdoptParams) []string {
	config, _, err := configpkg.ConfigStore{}.LoadEnvConfig(local.Tenant, local.Environment)
	if err != nil {
		return nil
	}
	return configpkg.PopulatedPortableEnvConfigFields(config)
}

// RunPlatformPushEnvDefinition uploads a local environment's portable settings to its hosted row.
func RunPlatformPushEnvDefinition(ctx core.Context, store configpkg.CloudReadStore, alias string, params model.EnvDefinitionPushParams, deps cloud.CloudDependencies) (EnvDefinitionPushResult, error) {
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

// RunPlatformPullEnvDefinition pulls the stored definition into a local environment.
// A nil confirm refuses a two-sided edit with the diff instead of overwriting.
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

// RunPlatformDeployEnvironment starts a server-side deploy of a registered environment.
func RunPlatformDeployEnvironment(ctx core.Context, store configpkg.CloudReadStore, alias, environmentID string, params model.PlatformDeployEnvironmentParams, deps cloud.CloudDependencies) (model.PlatformEnvironment, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformEnvironment{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "POST", "/v1/environments/"+environmentID+"/deploy", "version="+params.Version)
	if ctx.DryRun {
		return model.PlatformEnvironment{}, nil
	}
	return client.DeployEnvironment(context.Background(), environmentID, params)
}

// RunPlatformStopEnvironment scales a runtime environment to zero.
func RunPlatformStopEnvironment(ctx core.Context, store configpkg.CloudReadStore, alias, environmentID string, deps cloud.CloudDependencies) (model.PlatformEnvironment, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformEnvironment{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "POST", "/v1/environments/"+environmentID+"/stop")
	if ctx.DryRun {
		return model.PlatformEnvironment{}, nil
	}
	return client.StopEnvironment(context.Background(), environmentID)
}

// RunPlatformDeleteEnvironment starts an asynchronous teardown; the result reflects the claim.
func RunPlatformDeleteEnvironment(ctx core.Context, store configpkg.CloudReadStore, alias, environmentID string, deps cloud.CloudDependencies) (model.PlatformEnvironment, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformEnvironment{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "DELETE", "/v1/environments/"+environmentID)
	if ctx.DryRun {
		return model.PlatformEnvironment{}, nil
	}
	return client.DeleteEnvironment(context.Background(), environmentID)
}

// RunPlatformListContexts lists the caller's tenant's cloud contexts.
func RunPlatformListContexts(ctx core.Context, store configpkg.CloudReadStore, alias string, deps cloud.CloudDependencies) ([]model.PlatformContext, error) {
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
func RunPlatformGetContext(ctx core.Context, store configpkg.CloudReadStore, alias, contextID string, deps cloud.CloudDependencies) (model.PlatformContext, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformContext{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "GET", "/v1/contexts/"+contextID)
	if ctx.DryRun {
		return model.PlatformContext{}, nil
	}
	return client.GetContext(context.Background(), contextID)
}

// RunPlatformCreateContext registers a cloud context. params.Preview is a server-side dry
// run; ctx.DryRun skips the network call.
func RunPlatformCreateContext(ctx core.Context, store configpkg.CloudReadStore, alias string, params model.PlatformCreateContextParams, deps cloud.CloudDependencies) (model.PlatformCreateContextResult, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformCreateContextResult{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "POST", "/v1/contexts",
		"name="+params.Name, "alias="+params.CloudProviderAlias, "region="+params.Region, fmt.Sprintf("preview=%t", params.Preview))
	if ctx.DryRun {
		return model.PlatformCreateContextResult{}, nil
	}
	return client.CreateContext(context.Background(), params)
}

// RunPlatformProvision returns the ordered provisioning plan without executing it.
func RunPlatformProvision(ctx core.Context, store configpkg.CloudReadStore, alias string, params model.PlatformProvisionParams, deps cloud.CloudDependencies) (model.PlatformProvisionResult, error) {
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformProvisionResult{}, err
	}
	cloud.TracePlatformCall(ctx, provider, "POST", "/v1/provision", "env="+params.Environment.Name, "type="+params.Environment.Type)
	if ctx.DryRun {
		return model.PlatformProvisionResult{}, nil
	}
	return client.Provision(context.Background(), params)
}
