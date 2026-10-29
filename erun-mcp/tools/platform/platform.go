package platform

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/platform"
	"github.com/sophium/erun/erun-mcp/tools/shared/clouddeps"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

type PlatformWhoamiInput struct {
	toolio.PlatformAliasInput
}

type PlatformWhoamiResult struct {
	Preview bool                 `json:"preview"`
	Whoami  model.PlatformWhoami `json:"whoami,omitempty"`
	Trace   []string             `json:"trace,omitempty"`
}

func PlatformWhoamiTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformWhoamiInput) (*mcp.CallToolResult, PlatformWhoamiResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformWhoamiInput) (*mcp.CallToolResult, PlatformWhoamiResult, error) {
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_whoami"
		whoami, err := platform.RunPlatformWhoami(ctx, runtime.Store, input.Alias, clouddeps.CloudDependencies())
		if err != nil {
			return nil, PlatformWhoamiResult{}, err
		}
		return nil, PlatformWhoamiResult{Preview: input.Preview, Whoami: whoami, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformVersionInput struct {
	toolio.PlatformAliasInput
}

type PlatformVersionResult struct {
	Preview bool               `json:"preview"`
	Info    model.PlatformInfo `json:"info,omitempty"`
	Trace   []string           `json:"trace,omitempty"`
}

func PlatformVersionTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformVersionInput) (*mcp.CallToolResult, PlatformVersionResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformVersionInput) (*mcp.CallToolResult, PlatformVersionResult, error) {
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_version"
		info, err := platform.RunPlatformVersion(ctx, runtime.Store, input.Alias, clouddeps.CloudDependencies())
		if err != nil {
			return nil, PlatformVersionResult{}, err
		}
		return nil, PlatformVersionResult{Preview: input.Preview, Info: info, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformTenantCreateInput struct {
	toolio.PlatformAliasInput
	Name          string `json:"name" jsonschema:"tenant name (hyphen-free; forms the <tenant>-<env> namespace)"`
	Type          string `json:"type,omitempty" jsonschema:"tenant type: COMPANY (default) or OPERATIONS"`
	Issuer        string `json:"issuer" jsonschema:"OIDC issuer that resolves tokens to this tenant"`
	OrgFieldKey   string `json:"orgFieldKey,omitempty" jsonschema:"claim name that carries the org for a shared (multi-tenant) issuer"`
	OrgFieldValue string `json:"orgFieldValue,omitempty" jsonschema:"claim value identifying this tenant under a shared issuer"`
	DisplayName   string `json:"displayName,omitempty" jsonschema:"human-readable label for the tenant/issuer mapping; defaults to the issuer"`
}

type PlatformTenantResult struct {
	Preview bool                 `json:"preview"`
	Tenant  model.PlatformTenant `json:"tenant,omitempty"`
	Trace   []string             `json:"trace,omitempty"`
}

func PlatformTenantCreateTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformTenantCreateInput) (*mcp.CallToolResult, PlatformTenantResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformTenantCreateInput) (*mcp.CallToolResult, PlatformTenantResult, error) {
		if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Issuer) == "" {
			return nil, PlatformTenantResult{}, fmt.Errorf("name and issuer are required")
		}
		tenantType := input.Type
		if strings.TrimSpace(tenantType) == "" {
			tenantType = "COMPANY"
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_tenant_create"
		tenant, err := platform.RunPlatformCreateTenant(ctx, runtime.Store, input.Alias, model.PlatformCreateTenantParams{
			Name: input.Name, Type: tenantType, Issuer: input.Issuer,
			OrgFieldKey: input.OrgFieldKey, OrgFieldValue: input.OrgFieldValue, DisplayName: input.DisplayName,
		}, clouddeps.CloudDependencies())
		if err != nil {
			return nil, PlatformTenantResult{}, err
		}
		return nil, PlatformTenantResult{Preview: input.Preview, Tenant: tenant, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformTenantListInput struct {
	toolio.PlatformAliasInput
}

type PlatformTenantListResult struct {
	Preview bool                   `json:"preview"`
	Tenants []model.PlatformTenant `json:"tenants,omitempty"`
	Trace   []string               `json:"trace,omitempty"`
}

func PlatformTenantListTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformTenantListInput) (*mcp.CallToolResult, PlatformTenantListResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformTenantListInput) (*mcp.CallToolResult, PlatformTenantListResult, error) {
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_tenant_list"
		tenants, err := platform.RunPlatformListTenants(ctx, runtime.Store, input.Alias, clouddeps.CloudDependencies())
		if err != nil {
			return nil, PlatformTenantListResult{}, err
		}
		return nil, PlatformTenantListResult{Preview: input.Preview, Tenants: tenants, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformIdentityOrgCreateInput struct {
	toolio.PlatformAliasInput
	Name string `json:"name" jsonschema:"organization name"`
}

type PlatformIdentityOrgResult struct {
	Preview bool              `json:"preview"`
	Org     model.PlatformOrg `json:"org,omitempty"`
	Trace   []string          `json:"trace,omitempty"`
}

// PlatformIdentityOrgCreateTool creates an org on the platform's IdP; requires
// an operations-tenant caller.
func PlatformIdentityOrgCreateTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformIdentityOrgCreateInput) (*mcp.CallToolResult, PlatformIdentityOrgResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformIdentityOrgCreateInput) (*mcp.CallToolResult, PlatformIdentityOrgResult, error) {
		if strings.TrimSpace(input.Name) == "" {
			return nil, PlatformIdentityOrgResult{}, fmt.Errorf("name is required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_identity_org_create"
		org, err := platform.RunPlatformCreateOrg(ctx, runtime.Store, input.Alias, model.PlatformCreateOrgParams{Name: input.Name}, clouddeps.CloudDependencies())
		if err != nil {
			return nil, PlatformIdentityOrgResult{}, err
		}
		return nil, PlatformIdentityOrgResult{Preview: input.Preview, Org: org, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformTenantRepairOrgMappingInput struct {
	toolio.PlatformAliasInput
	TenantID      string `json:"tenantId,omitempty" jsonschema:"tenant to repair (operations-tenant callers only); defaults to the caller's own tenant"`
	Issuer        string `json:"issuer" jsonschema:"OIDC issuer the tenant is mapped under"`
	OrgFieldKey   string `json:"orgFieldKey" jsonschema:"claim name that carries the org for this shared issuer"`
	OrgFieldValue string `json:"orgFieldValue" jsonschema:"org value to set on the tenant's mapping (see platform_identity_org_create)"`
}

type PlatformTenantIssuerResult struct {
	Preview      bool                       `json:"preview"`
	TenantIssuer model.PlatformTenantIssuer `json:"tenantIssuer,omitempty"`
	Trace        []string                   `json:"trace,omitempty"`
}

// PlatformTenantRepairOrgMappingTool fixes an unresolvable (issuer, org)
// mapping; the platform has no tenant delete.
func PlatformTenantRepairOrgMappingTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformTenantRepairOrgMappingInput) (*mcp.CallToolResult, PlatformTenantIssuerResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformTenantRepairOrgMappingInput) (*mcp.CallToolResult, PlatformTenantIssuerResult, error) {
		if strings.TrimSpace(input.Issuer) == "" || strings.TrimSpace(input.OrgFieldKey) == "" || strings.TrimSpace(input.OrgFieldValue) == "" {
			return nil, PlatformTenantIssuerResult{}, fmt.Errorf("issuer, orgFieldKey, and orgFieldValue are required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_tenant_repair-org-mapping"
		issuer, err := platform.RunPlatformRepairTenantIssuerOrgMapping(ctx, runtime.Store, input.Alias, model.PlatformRepairTenantIssuerOrgMappingParams{
			TenantID: input.TenantID, Issuer: input.Issuer, OrgFieldKey: input.OrgFieldKey, OrgFieldValue: input.OrgFieldValue,
		}, clouddeps.CloudDependencies())
		if err != nil {
			return nil, PlatformTenantIssuerResult{}, err
		}
		return nil, PlatformTenantIssuerResult{Preview: input.Preview, TenantIssuer: issuer, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformUserEnrollInput struct {
	toolio.PlatformAliasInput
	Username string   `json:"username" jsonschema:"username to enroll"`
	Issuer   string   `json:"issuer,omitempty" jsonschema:"OIDC issuer of the external identity to link"`
	Subject  string   `json:"subject,omitempty" jsonschema:"OIDC subject of the external identity to link"`
	TenantID string   `json:"tenantId,omitempty" jsonschema:"target tenant id (operations-tenant callers only); defaults to the caller's own tenant"`
	RoleIDs  []string `json:"roleIds,omitempty" jsonschema:"role ids to grant instead of the platform's default role for this enrollment"`
}

type PlatformUserResult struct {
	Preview bool               `json:"preview"`
	User    model.PlatformUser `json:"user,omitempty"`
	Trace   []string           `json:"trace,omitempty"`
}

func PlatformUserEnrollTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformUserEnrollInput) (*mcp.CallToolResult, PlatformUserResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformUserEnrollInput) (*mcp.CallToolResult, PlatformUserResult, error) {
		if strings.TrimSpace(input.Username) == "" {
			return nil, PlatformUserResult{}, fmt.Errorf("username is required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_user_enroll"
		user, err := platform.RunPlatformCreateUser(ctx, runtime.Store, input.Alias, model.PlatformCreateUserParams{
			Username: input.Username, Issuer: input.Issuer, Subject: input.Subject, TenantID: input.TenantID,
			RoleIDs: input.RoleIDs,
		}, clouddeps.CloudDependencies())
		if err != nil {
			return nil, PlatformUserResult{}, err
		}
		return nil, PlatformUserResult{Preview: input.Preview, User: user, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformUserGrantRoleInput struct {
	toolio.PlatformAliasInput
	UserID string `json:"userId" jsonschema:"id of the already-enrolled user to grant the role to"`
	RoleID string `json:"roleId" jsonschema:"id of the role to grant, from the tenant's role list"`
}

type PlatformUserGrantRoleResult struct {
	Preview bool     `json:"preview"`
	UserID  string   `json:"userId"`
	RoleID  string   `json:"roleId"`
	Trace   []string `json:"trace,omitempty"`
}

func PlatformUserGrantRoleTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformUserGrantRoleInput) (*mcp.CallToolResult, PlatformUserGrantRoleResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformUserGrantRoleInput) (*mcp.CallToolResult, PlatformUserGrantRoleResult, error) {
		if strings.TrimSpace(input.UserID) == "" {
			return nil, PlatformUserGrantRoleResult{}, fmt.Errorf("userId is required")
		}
		if strings.TrimSpace(input.RoleID) == "" {
			return nil, PlatformUserGrantRoleResult{}, fmt.Errorf("roleId is required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_user_grant-role"
		err := platform.RunPlatformGrantUserRole(ctx, runtime.Store, input.Alias, model.PlatformGrantUserRoleParams{
			UserID: input.UserID, RoleID: input.RoleID,
		}, clouddeps.CloudDependencies())
		if err != nil {
			return nil, PlatformUserGrantRoleResult{}, err
		}
		return nil, PlatformUserGrantRoleResult{Preview: input.Preview, UserID: input.UserID, RoleID: input.RoleID, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformUserListInput struct {
	toolio.PlatformAliasInput
	TenantID string `json:"tenantId,omitempty" jsonschema:"target tenant id (operations-tenant callers only); defaults to the caller's own tenant"`
}

type PlatformUserListResult struct {
	Preview bool                 `json:"preview"`
	Users   []model.PlatformUser `json:"users,omitempty"`
	Trace   []string             `json:"trace,omitempty"`
}

func PlatformUserListTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformUserListInput) (*mcp.CallToolResult, PlatformUserListResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformUserListInput) (*mcp.CallToolResult, PlatformUserListResult, error) {
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_user_list"
		users, err := platform.RunPlatformListUsers(ctx, runtime.Store, input.Alias, model.PlatformListUsersParams{TenantID: input.TenantID}, clouddeps.CloudDependencies())
		if err != nil {
			return nil, PlatformUserListResult{}, err
		}
		return nil, PlatformUserListResult{Preview: input.Preview, Users: users, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformEnvListInput struct {
	toolio.PlatformAliasInput
}

type PlatformEnvListResult struct {
	Preview      bool                        `json:"preview"`
	Environments []model.PlatformEnvironment `json:"environments,omitempty"`
	Trace        []string                    `json:"trace,omitempty"`
}

func PlatformEnvListTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformEnvListInput) (*mcp.CallToolResult, PlatformEnvListResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformEnvListInput) (*mcp.CallToolResult, PlatformEnvListResult, error) {
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_env_list"
		environments, err := platform.RunPlatformListEnvironments(ctx, runtime.Store, input.Alias, clouddeps.CloudDependencies())
		if err != nil {
			return nil, PlatformEnvListResult{}, err
		}
		return nil, PlatformEnvListResult{Preview: input.Preview, Environments: environments, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformEnvGetInput struct {
	toolio.PlatformAliasInput
	EnvironmentID string `json:"environmentId" jsonschema:"environment id to fetch"`
}

type PlatformEnvResult struct {
	Preview     bool                      `json:"preview"`
	Environment model.PlatformEnvironment `json:"environment,omitempty"`
	Trace       []string                  `json:"trace,omitempty"`
}

func PlatformEnvGetTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformEnvGetInput) (*mcp.CallToolResult, PlatformEnvResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformEnvGetInput) (*mcp.CallToolResult, PlatformEnvResult, error) {
		if strings.TrimSpace(input.EnvironmentID) == "" {
			return nil, PlatformEnvResult{}, fmt.Errorf("environmentId is required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_env_get"
		environment, err := platform.RunPlatformGetEnvironment(ctx, runtime.Store, input.Alias, input.EnvironmentID, clouddeps.CloudDependencies())
		if err != nil {
			return nil, PlatformEnvResult{}, err
		}
		return nil, PlatformEnvResult{Preview: input.Preview, Environment: environment, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformEnvRegisterInput struct {
	toolio.PlatformAliasInput
	Name              string `json:"name" jsonschema:"environment name (DNS-1123 label; forms the <tenant>-<env> namespace)"`
	Type              string `json:"type" jsonschema:"environment type: runtime, remote-agent, or local-agent"`
	ContextID         string `json:"contextId,omitempty" jsonschema:"cloud context to deploy into (see platform_context_list)"`
	KubernetesContext string `json:"kubernetesContext,omitempty" jsonschema:"kubernetes context name to deploy into, if not using contextId"`
	RuntimeVersion    string `json:"runtimeVersion,omitempty" jsonschema:"published erun runtime version to deploy (runtime environments only); when set on a runtime environment and the platform has a deploy executor configured, this also starts a server-side deploy"`
}

func PlatformEnvRegisterTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformEnvRegisterInput) (*mcp.CallToolResult, PlatformEnvResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformEnvRegisterInput) (*mcp.CallToolResult, PlatformEnvResult, error) {
		if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Type) == "" {
			return nil, PlatformEnvResult{}, fmt.Errorf("name and type are required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_env_register"
		environment, err := platform.RunPlatformRegisterEnvironment(ctx, runtime.Store, input.Alias, model.PlatformCreateEnvironmentParams{
			Name: input.Name, Type: input.Type, ContextID: input.ContextID,
			KubernetesContext: input.KubernetesContext, RuntimeVersion: input.RuntimeVersion,
		}, clouddeps.CloudDependencies())
		if err != nil {
			return nil, PlatformEnvResult{}, err
		}
		return nil, PlatformEnvResult{Preview: input.Preview, Environment: environment, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformEnvDeployInput struct {
	toolio.PlatformAliasInput
	EnvironmentID string `json:"environmentId" jsonschema:"environment id to deploy"`
	Version       string `json:"version,omitempty" jsonschema:"published version to deploy; defaults to the environment's pinned runtime version"`
}

func PlatformEnvDeployTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformEnvDeployInput) (*mcp.CallToolResult, PlatformEnvResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformEnvDeployInput) (*mcp.CallToolResult, PlatformEnvResult, error) {
		if strings.TrimSpace(input.EnvironmentID) == "" {
			return nil, PlatformEnvResult{}, fmt.Errorf("environmentId is required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_env_deploy"
		environment, err := platform.RunPlatformDeployEnvironment(ctx, runtime.Store, input.Alias, input.EnvironmentID, model.PlatformDeployEnvironmentParams{Version: input.Version}, clouddeps.CloudDependencies())
		if err != nil {
			return nil, PlatformEnvResult{}, err
		}
		return nil, PlatformEnvResult{Preview: input.Preview, Environment: environment, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformEnvStopInput struct {
	toolio.PlatformAliasInput
	EnvironmentID string `json:"environmentId" jsonschema:"environment id to stop"`
}

func PlatformEnvStopTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformEnvStopInput) (*mcp.CallToolResult, PlatformEnvResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformEnvStopInput) (*mcp.CallToolResult, PlatformEnvResult, error) {
		if strings.TrimSpace(input.EnvironmentID) == "" {
			return nil, PlatformEnvResult{}, fmt.Errorf("environmentId is required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_env_stop"
		environment, err := platform.RunPlatformStopEnvironment(ctx, runtime.Store, input.Alias, input.EnvironmentID, clouddeps.CloudDependencies())
		if err != nil {
			return nil, PlatformEnvResult{}, err
		}
		return nil, PlatformEnvResult{Preview: input.Preview, Environment: environment, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformEnvDeleteInput struct {
	toolio.PlatformAliasInput
	EnvironmentID string `json:"environmentId" jsonschema:"environment id to delete"`
	// Confirm stands in for the CLI's prompt; MCP never prompts.
	Confirm bool `json:"confirm" jsonschema:"must be true to actually delete; required because this call never prompts. Ignored when preview is true."`
}

// PlatformEnvDeleteTool returns once the delete is claimed, since a stuck
// finalizer can wedge it indefinitely; poll platform_env_get, and call again to retry.
func PlatformEnvDeleteTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformEnvDeleteInput) (*mcp.CallToolResult, PlatformEnvResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformEnvDeleteInput) (*mcp.CallToolResult, PlatformEnvResult, error) {
		if strings.TrimSpace(input.EnvironmentID) == "" {
			return nil, PlatformEnvResult{}, fmt.Errorf("environmentId is required")
		}
		if !input.Preview && !input.Confirm {
			return nil, PlatformEnvResult{}, fmt.Errorf("confirm must be true to delete environment %s; this call never prompts", input.EnvironmentID)
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_env_delete"
		environment, err := platform.RunPlatformDeleteEnvironment(ctx, runtime.Store, input.Alias, input.EnvironmentID, clouddeps.CloudDependencies())
		if err != nil {
			return nil, PlatformEnvResult{}, err
		}
		return nil, PlatformEnvResult{Preview: input.Preview, Environment: environment, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformContextListInput struct {
	toolio.PlatformAliasInput
}

type PlatformContextListResult struct {
	Preview  bool                    `json:"preview"`
	Contexts []model.PlatformContext `json:"contexts,omitempty"`
	Trace    []string                `json:"trace,omitempty"`
}

func PlatformContextListTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformContextListInput) (*mcp.CallToolResult, PlatformContextListResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformContextListInput) (*mcp.CallToolResult, PlatformContextListResult, error) {
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_context_list"
		contexts, err := platform.RunPlatformListContexts(ctx, runtime.Store, input.Alias, clouddeps.CloudDependencies())
		if err != nil {
			return nil, PlatformContextListResult{}, err
		}
		return nil, PlatformContextListResult{Preview: input.Preview, Contexts: contexts, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformContextGetInput struct {
	toolio.PlatformAliasInput
	ContextID string `json:"contextId" jsonschema:"cloud context id to fetch"`
}

type PlatformContextResult struct {
	Preview bool                  `json:"preview"`
	Context model.PlatformContext `json:"context,omitempty"`
	Trace   []string              `json:"trace,omitempty"`
}

func PlatformContextGetTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformContextGetInput) (*mcp.CallToolResult, PlatformContextResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformContextGetInput) (*mcp.CallToolResult, PlatformContextResult, error) {
		if strings.TrimSpace(input.ContextID) == "" {
			return nil, PlatformContextResult{}, fmt.Errorf("contextId is required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_context_get"
		cloudContext, err := platform.RunPlatformGetContext(ctx, runtime.Store, input.Alias, input.ContextID, clouddeps.CloudDependencies())
		if err != nil {
			return nil, PlatformContextResult{}, err
		}
		return nil, PlatformContextResult{Preview: input.Preview, Context: cloudContext, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformContextCreateInput struct {
	toolio.PlatformAliasInput
	Name               string `json:"name" jsonschema:"kubernetes context name to create"`
	CloudProviderAlias string `json:"cloudProviderAlias" jsonschema:"cloud provider alias (on the tenant's own account) to bootstrap with"`
	Region             string `json:"region" jsonschema:"cloud region for the context"`
	InstanceType       string `json:"instanceType,omitempty" jsonschema:"instance type for the context's VM"`
	DiskType           string `json:"diskType,omitempty" jsonschema:"root disk type"`
	DiskSizeGB         int    `json:"diskSizeGb,omitempty" jsonschema:"root disk size in GB"`
	// PlanOnly is the platform's server-side preview; Preview skips the network.
	PlanOnly bool `json:"planOnly,omitempty" jsonschema:"ask the platform to resolve and return the bootstrap plan without creating anything, still a real API call unlike preview"`
}

type PlatformContextCreateResult struct {
	Preview bool                              `json:"preview"`
	Result  model.PlatformCreateContextResult `json:"result,omitempty"`
	Trace   []string                          `json:"trace,omitempty"`
}

func PlatformContextCreateTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformContextCreateInput) (*mcp.CallToolResult, PlatformContextCreateResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformContextCreateInput) (*mcp.CallToolResult, PlatformContextCreateResult, error) {
		if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.CloudProviderAlias) == "" || strings.TrimSpace(input.Region) == "" {
			return nil, PlatformContextCreateResult{}, fmt.Errorf("name, cloudProviderAlias, and region are required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_context_create"
		result, err := platform.RunPlatformCreateContext(ctx, runtime.Store, input.Alias, model.PlatformCreateContextParams{
			Name: input.Name, CloudProviderAlias: input.CloudProviderAlias, Region: input.Region,
			InstanceType: input.InstanceType, DiskType: input.DiskType, DiskSizeGB: input.DiskSizeGB, Preview: input.PlanOnly,
		}, clouddeps.CloudDependencies())
		if err != nil {
			return nil, PlatformContextCreateResult{}, err
		}
		return nil, PlatformContextCreateResult{Preview: input.Preview, Result: result, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformProvisionInput struct {
	toolio.PlatformAliasInput
	EnvName             string `json:"envName" jsonschema:"environment name to provision"`
	EnvType             string `json:"envType" jsonschema:"environment type: runtime, remote-agent, or local-agent"`
	KubernetesContext   string `json:"kubernetesContext,omitempty" jsonschema:"reuse an existing kubernetes context instead of bootstrapping one"`
	ContextName         string `json:"contextName,omitempty" jsonschema:"name for a new cloud context to bootstrap; set together with contextAlias and contextRegion instead of kubernetesContext"`
	ContextAlias        string `json:"contextAlias,omitempty" jsonschema:"cloud provider alias to bootstrap the new context with"`
	ContextRegion       string `json:"contextRegion,omitempty" jsonschema:"cloud region for the new context"`
	ContextInstanceType string `json:"contextInstanceType,omitempty" jsonschema:"instance type for the new context's VM"`
	ContextDiskType     string `json:"contextDiskType,omitempty" jsonschema:"root disk type for the new context"`
	ContextDiskSizeGB   int    `json:"contextDiskSizeGb,omitempty" jsonschema:"root disk size in GB for the new context"`
}

type PlatformProvisionResult struct {
	Preview bool                          `json:"preview"`
	Result  model.PlatformProvisionResult `json:"result,omitempty"`
	Trace   []string                      `json:"trace,omitempty"`
}

func PlatformProvisionTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformProvisionInput) (*mcp.CallToolResult, PlatformProvisionResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformProvisionInput) (*mcp.CallToolResult, PlatformProvisionResult, error) {
		if strings.TrimSpace(input.EnvName) == "" || strings.TrimSpace(input.EnvType) == "" {
			return nil, PlatformProvisionResult{}, fmt.Errorf("envName and envType are required")
		}
		params := model.PlatformProvisionParams{
			Environment:       model.PlatformProvisionEnvironment{Name: input.EnvName, Type: input.EnvType},
			KubernetesContext: input.KubernetesContext,
		}
		if strings.TrimSpace(input.ContextName) != "" {
			params.Context = &model.PlatformProvisionContext{
				Name: input.ContextName, CloudProviderAlias: input.ContextAlias, Region: input.ContextRegion,
				InstanceType: input.ContextInstanceType, DiskType: input.ContextDiskType, DiskSizeGB: input.ContextDiskSizeGB,
			}
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_provision"
		result, err := platform.RunPlatformProvision(ctx, runtime.Store, input.Alias, params, clouddeps.CloudDependencies())
		if err != nil {
			return nil, PlatformProvisionResult{}, err
		}
		return nil, PlatformProvisionResult{Preview: input.Preview, Result: result, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}
