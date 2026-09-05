package erunmcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// platform.go exposes `erun platform`'s operations as MCP tools over the same
// shared eruncommon.RunPlatform* functions the CLI drives (erun-cli/cmd/platform.go),
// so the two transports can never disagree about what a call does. Preview
// (this module's non-interactive analogue of --dry-run) reuses the exact same
// trace: every tool runs its RunPlatform* call with Preview forwarded straight
// into eruncommon.Context.DryRun, so the resolved HTTP call is traced and no
// network request is sent, with no separately hand-maintained plan text to
// drift from the real path.

type platformAliasInput struct {
	Alias     string `json:"alias,omitempty" jsonschema:"erun platform cloud alias to target; defaults to the sole configured erun-type alias"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, trace the resolved call without sending it"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

type PlatformWhoamiInput struct {
	platformAliasInput
}

type PlatformWhoamiResult struct {
	Preview bool                      `json:"preview"`
	Whoami  eruncommon.PlatformWhoami `json:"whoami,omitempty"`
	Trace   []string                  `json:"trace,omitempty"`
}

func platformWhoamiTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformWhoamiInput) (*mcp.CallToolResult, PlatformWhoamiResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformWhoamiInput) (*mcp.CallToolResult, PlatformWhoamiResult, error) {
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_whoami"
		whoami, err := eruncommon.RunPlatformWhoami(ctx, runtime.Store, input.Alias, cloudDependencies())
		if err != nil {
			return nil, PlatformWhoamiResult{}, err
		}
		return nil, PlatformWhoamiResult{Preview: input.Preview, Whoami: whoami, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformVersionInput struct {
	platformAliasInput
}

type PlatformVersionResult struct {
	Preview bool                    `json:"preview"`
	Info    eruncommon.PlatformInfo `json:"info,omitempty"`
	Trace   []string                `json:"trace,omitempty"`
}

func platformVersionTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformVersionInput) (*mcp.CallToolResult, PlatformVersionResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformVersionInput) (*mcp.CallToolResult, PlatformVersionResult, error) {
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_version"
		info, err := eruncommon.RunPlatformVersion(ctx, runtime.Store, input.Alias, cloudDependencies())
		if err != nil {
			return nil, PlatformVersionResult{}, err
		}
		return nil, PlatformVersionResult{Preview: input.Preview, Info: info, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformTenantCreateInput struct {
	platformAliasInput
	Name          string `json:"name" jsonschema:"tenant name (hyphen-free; forms the <tenant>-<env> namespace)"`
	Type          string `json:"type,omitempty" jsonschema:"tenant type: COMPANY (default) or OPERATIONS"`
	Issuer        string `json:"issuer" jsonschema:"OIDC issuer that resolves tokens to this tenant"`
	OrgFieldKey   string `json:"orgFieldKey,omitempty" jsonschema:"claim name that carries the org for a shared (multi-tenant) issuer"`
	OrgFieldValue string `json:"orgFieldValue,omitempty" jsonschema:"claim value identifying this tenant under a shared issuer"`
	DisplayName   string `json:"displayName,omitempty" jsonschema:"human-readable label for the tenant/issuer mapping; defaults to the issuer"`
}

type PlatformTenantResult struct {
	Preview bool                      `json:"preview"`
	Tenant  eruncommon.PlatformTenant `json:"tenant,omitempty"`
	Trace   []string                  `json:"trace,omitempty"`
}

func platformTenantCreateTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformTenantCreateInput) (*mcp.CallToolResult, PlatformTenantResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformTenantCreateInput) (*mcp.CallToolResult, PlatformTenantResult, error) {
		if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Issuer) == "" {
			return nil, PlatformTenantResult{}, fmt.Errorf("name and issuer are required")
		}
		tenantType := input.Type
		if strings.TrimSpace(tenantType) == "" {
			tenantType = "COMPANY"
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_tenant_create"
		tenant, err := eruncommon.RunPlatformCreateTenant(ctx, runtime.Store, input.Alias, eruncommon.PlatformCreateTenantParams{
			Name: input.Name, Type: tenantType, Issuer: input.Issuer,
			OrgFieldKey: input.OrgFieldKey, OrgFieldValue: input.OrgFieldValue, DisplayName: input.DisplayName,
		}, cloudDependencies())
		if err != nil {
			return nil, PlatformTenantResult{}, err
		}
		return nil, PlatformTenantResult{Preview: input.Preview, Tenant: tenant, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformTenantListInput struct {
	platformAliasInput
}

type PlatformTenantListResult struct {
	Preview bool                        `json:"preview"`
	Tenants []eruncommon.PlatformTenant `json:"tenants,omitempty"`
	Trace   []string                    `json:"trace,omitempty"`
}

func platformTenantListTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformTenantListInput) (*mcp.CallToolResult, PlatformTenantListResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformTenantListInput) (*mcp.CallToolResult, PlatformTenantListResult, error) {
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_tenant_list"
		tenants, err := eruncommon.RunPlatformListTenants(ctx, runtime.Store, input.Alias, cloudDependencies())
		if err != nil {
			return nil, PlatformTenantListResult{}, err
		}
		return nil, PlatformTenantListResult{Preview: input.Preview, Tenants: tenants, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformIdentityOrgCreateInput struct {
	platformAliasInput
	Name string `json:"name" jsonschema:"organization name"`
}

type PlatformIdentityOrgResult struct {
	Preview bool                   `json:"preview"`
	Org     eruncommon.PlatformOrg `json:"org,omitempty"`
	Trace   []string               `json:"trace,omitempty"`
}

// platformIdentityOrgCreateTool creates an organization on the platform's own
// IdP -- the org an org-scoped tenant mapping needs before platform_tenant_create
// can produce one any token will ever resolve to. Requires an
// operations-tenant caller.
func platformIdentityOrgCreateTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformIdentityOrgCreateInput) (*mcp.CallToolResult, PlatformIdentityOrgResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformIdentityOrgCreateInput) (*mcp.CallToolResult, PlatformIdentityOrgResult, error) {
		if strings.TrimSpace(input.Name) == "" {
			return nil, PlatformIdentityOrgResult{}, fmt.Errorf("name is required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_identity_org_create"
		org, err := eruncommon.RunPlatformCreateOrg(ctx, runtime.Store, input.Alias, eruncommon.PlatformCreateOrgParams{Name: input.Name}, cloudDependencies())
		if err != nil {
			return nil, PlatformIdentityOrgResult{}, err
		}
		return nil, PlatformIdentityOrgResult{Preview: input.Preview, Org: org, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformTenantRepairOrgMappingInput struct {
	platformAliasInput
	TenantID      string `json:"tenantId,omitempty" jsonschema:"tenant to repair (operations-tenant callers only); defaults to the caller's own tenant"`
	Issuer        string `json:"issuer" jsonschema:"OIDC issuer the tenant is mapped under"`
	OrgFieldKey   string `json:"orgFieldKey" jsonschema:"claim name that carries the org for this shared issuer"`
	OrgFieldValue string `json:"orgFieldValue" jsonschema:"org value to set on the tenant's mapping (see platform_identity_org_create)"`
}

type PlatformTenantIssuerResult struct {
	Preview      bool                            `json:"preview"`
	TenantIssuer eruncommon.PlatformTenantIssuer `json:"tenantIssuer,omitempty"`
	Trace        []string                        `json:"trace,omitempty"`
}

// platformTenantRepairOrgMappingTool fixes a tenant already stuck with an
// unresolvable (issuer, org) mapping -- a tenant that lists but that no token
// can ever authenticate into. There is no tenant delete on the platform at
// all, so this is the only way back short of direct database access.
func platformTenantRepairOrgMappingTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformTenantRepairOrgMappingInput) (*mcp.CallToolResult, PlatformTenantIssuerResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformTenantRepairOrgMappingInput) (*mcp.CallToolResult, PlatformTenantIssuerResult, error) {
		if strings.TrimSpace(input.Issuer) == "" || strings.TrimSpace(input.OrgFieldKey) == "" || strings.TrimSpace(input.OrgFieldValue) == "" {
			return nil, PlatformTenantIssuerResult{}, fmt.Errorf("issuer, orgFieldKey, and orgFieldValue are required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_tenant_repair-org-mapping"
		issuer, err := eruncommon.RunPlatformRepairTenantIssuerOrgMapping(ctx, runtime.Store, input.Alias, eruncommon.PlatformRepairTenantIssuerOrgMappingParams{
			TenantID: input.TenantID, Issuer: input.Issuer, OrgFieldKey: input.OrgFieldKey, OrgFieldValue: input.OrgFieldValue,
		}, cloudDependencies())
		if err != nil {
			return nil, PlatformTenantIssuerResult{}, err
		}
		return nil, PlatformTenantIssuerResult{Preview: input.Preview, TenantIssuer: issuer, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformUserEnrollInput struct {
	platformAliasInput
	Username string   `json:"username" jsonschema:"username to enroll"`
	Issuer   string   `json:"issuer,omitempty" jsonschema:"OIDC issuer of the external identity to link"`
	Subject  string   `json:"subject,omitempty" jsonschema:"OIDC subject of the external identity to link"`
	TenantID string   `json:"tenantId,omitempty" jsonschema:"target tenant id (operations-tenant callers only); defaults to the caller's own tenant"`
	RoleIDs  []string `json:"roleIds,omitempty" jsonschema:"role ids to grant instead of the platform's default role for this enrollment"`
}

type PlatformUserResult struct {
	Preview bool                    `json:"preview"`
	User    eruncommon.PlatformUser `json:"user,omitempty"`
	Trace   []string                `json:"trace,omitempty"`
}

func platformUserEnrollTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformUserEnrollInput) (*mcp.CallToolResult, PlatformUserResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformUserEnrollInput) (*mcp.CallToolResult, PlatformUserResult, error) {
		if strings.TrimSpace(input.Username) == "" {
			return nil, PlatformUserResult{}, fmt.Errorf("username is required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_user_enroll"
		user, err := eruncommon.RunPlatformCreateUser(ctx, runtime.Store, input.Alias, eruncommon.PlatformCreateUserParams{
			Username: input.Username, Issuer: input.Issuer, Subject: input.Subject, TenantID: input.TenantID,
			RoleIDs: input.RoleIDs,
		}, cloudDependencies())
		if err != nil {
			return nil, PlatformUserResult{}, err
		}
		return nil, PlatformUserResult{Preview: input.Preview, User: user, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformUserListInput struct {
	platformAliasInput
	TenantID string `json:"tenantId,omitempty" jsonschema:"target tenant id (operations-tenant callers only); defaults to the caller's own tenant"`
}

type PlatformUserListResult struct {
	Preview bool                      `json:"preview"`
	Users   []eruncommon.PlatformUser `json:"users,omitempty"`
	Trace   []string                  `json:"trace,omitempty"`
}

func platformUserListTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformUserListInput) (*mcp.CallToolResult, PlatformUserListResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformUserListInput) (*mcp.CallToolResult, PlatformUserListResult, error) {
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_user_list"
		users, err := eruncommon.RunPlatformListUsers(ctx, runtime.Store, input.Alias, eruncommon.PlatformListUsersParams{TenantID: input.TenantID}, cloudDependencies())
		if err != nil {
			return nil, PlatformUserListResult{}, err
		}
		return nil, PlatformUserListResult{Preview: input.Preview, Users: users, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformEnvListInput struct {
	platformAliasInput
}

type PlatformEnvListResult struct {
	Preview      bool                             `json:"preview"`
	Environments []eruncommon.PlatformEnvironment `json:"environments,omitempty"`
	Trace        []string                         `json:"trace,omitempty"`
}

func platformEnvListTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformEnvListInput) (*mcp.CallToolResult, PlatformEnvListResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformEnvListInput) (*mcp.CallToolResult, PlatformEnvListResult, error) {
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_env_list"
		environments, err := eruncommon.RunPlatformListEnvironments(ctx, runtime.Store, input.Alias, cloudDependencies())
		if err != nil {
			return nil, PlatformEnvListResult{}, err
		}
		return nil, PlatformEnvListResult{Preview: input.Preview, Environments: environments, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformEnvGetInput struct {
	platformAliasInput
	EnvironmentID string `json:"environmentId" jsonschema:"environment id to fetch"`
}

type PlatformEnvResult struct {
	Preview     bool                           `json:"preview"`
	Environment eruncommon.PlatformEnvironment `json:"environment,omitempty"`
	Trace       []string                       `json:"trace,omitempty"`
}

func platformEnvGetTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformEnvGetInput) (*mcp.CallToolResult, PlatformEnvResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformEnvGetInput) (*mcp.CallToolResult, PlatformEnvResult, error) {
		if strings.TrimSpace(input.EnvironmentID) == "" {
			return nil, PlatformEnvResult{}, fmt.Errorf("environmentId is required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_env_get"
		environment, err := eruncommon.RunPlatformGetEnvironment(ctx, runtime.Store, input.Alias, input.EnvironmentID, cloudDependencies())
		if err != nil {
			return nil, PlatformEnvResult{}, err
		}
		return nil, PlatformEnvResult{Preview: input.Preview, Environment: environment, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformEnvRegisterInput struct {
	platformAliasInput
	Name              string `json:"name" jsonschema:"environment name (DNS-1123 label; forms the <tenant>-<env> namespace)"`
	Type              string `json:"type" jsonschema:"environment type: runtime, remote-agent, or local-agent"`
	ContextID         string `json:"contextId,omitempty" jsonschema:"cloud context to deploy into (see platform_context_list)"`
	KubernetesContext string `json:"kubernetesContext,omitempty" jsonschema:"kubernetes context name to deploy into, if not using contextId"`
	RuntimeVersion    string `json:"runtimeVersion,omitempty" jsonschema:"published erun runtime version to deploy (runtime environments only); when set on a runtime environment and the platform has a deploy executor configured, this also starts a server-side deploy"`
}

func platformEnvRegisterTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformEnvRegisterInput) (*mcp.CallToolResult, PlatformEnvResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformEnvRegisterInput) (*mcp.CallToolResult, PlatformEnvResult, error) {
		if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Type) == "" {
			return nil, PlatformEnvResult{}, fmt.Errorf("name and type are required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_env_register"
		environment, err := eruncommon.RunPlatformRegisterEnvironment(ctx, runtime.Store, input.Alias, eruncommon.PlatformCreateEnvironmentParams{
			Name: input.Name, Type: input.Type, ContextID: input.ContextID,
			KubernetesContext: input.KubernetesContext, RuntimeVersion: input.RuntimeVersion,
		}, cloudDependencies())
		if err != nil {
			return nil, PlatformEnvResult{}, err
		}
		return nil, PlatformEnvResult{Preview: input.Preview, Environment: environment, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformEnvDeployInput struct {
	platformAliasInput
	EnvironmentID string `json:"environmentId" jsonschema:"environment id to deploy"`
	Version       string `json:"version,omitempty" jsonschema:"published version to deploy; defaults to the environment's pinned runtime version"`
}

func platformEnvDeployTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformEnvDeployInput) (*mcp.CallToolResult, PlatformEnvResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformEnvDeployInput) (*mcp.CallToolResult, PlatformEnvResult, error) {
		if strings.TrimSpace(input.EnvironmentID) == "" {
			return nil, PlatformEnvResult{}, fmt.Errorf("environmentId is required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_env_deploy"
		environment, err := eruncommon.RunPlatformDeployEnvironment(ctx, runtime.Store, input.Alias, input.EnvironmentID, eruncommon.PlatformDeployEnvironmentParams{Version: input.Version}, cloudDependencies())
		if err != nil {
			return nil, PlatformEnvResult{}, err
		}
		return nil, PlatformEnvResult{Preview: input.Preview, Environment: environment, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformEnvStopInput struct {
	platformAliasInput
	EnvironmentID string `json:"environmentId" jsonschema:"environment id to stop"`
}

func platformEnvStopTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformEnvStopInput) (*mcp.CallToolResult, PlatformEnvResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformEnvStopInput) (*mcp.CallToolResult, PlatformEnvResult, error) {
		if strings.TrimSpace(input.EnvironmentID) == "" {
			return nil, PlatformEnvResult{}, fmt.Errorf("environmentId is required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_env_stop"
		environment, err := eruncommon.RunPlatformStopEnvironment(ctx, runtime.Store, input.Alias, input.EnvironmentID, cloudDependencies())
		if err != nil {
			return nil, PlatformEnvResult{}, err
		}
		return nil, PlatformEnvResult{Preview: input.Preview, Environment: environment, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformEnvDeleteInput struct {
	platformAliasInput
	EnvironmentID string `json:"environmentId" jsonschema:"environment id to delete"`
	// Confirm replaces the CLI's interactive confirmation prompt: MCP-exposed
	// paths never prompt, so the caller must explicitly opt in to the
	// irreversible action.
	Confirm bool `json:"confirm" jsonschema:"must be true to actually delete; required because this call never prompts. Ignored when preview is true."`
}

// platformEnvDeleteTool starts an environment's delete and returns
// immediately rather than waiting for the namespace to actually disappear
// (#1140): a namespace stuck on an unsatisfiable finalizer can otherwise wedge
// for as long as Kubernetes is willing to sit in Terminating. The returned
// Environment reflects the claim (status "deleting"); call platform_env_get
// to watch it converge to gone (not found) or "deletion-blocked" (its
// deleteError names why). Calling delete again against a blocked or
// still-deleting environment retries it.
func platformEnvDeleteTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformEnvDeleteInput) (*mcp.CallToolResult, PlatformEnvResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformEnvDeleteInput) (*mcp.CallToolResult, PlatformEnvResult, error) {
		if strings.TrimSpace(input.EnvironmentID) == "" {
			return nil, PlatformEnvResult{}, fmt.Errorf("environmentId is required")
		}
		if !input.Preview && !input.Confirm {
			return nil, PlatformEnvResult{}, fmt.Errorf("confirm must be true to delete environment %s; this call never prompts", input.EnvironmentID)
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_env_delete"
		environment, err := eruncommon.RunPlatformDeleteEnvironment(ctx, runtime.Store, input.Alias, input.EnvironmentID, cloudDependencies())
		if err != nil {
			return nil, PlatformEnvResult{}, err
		}
		return nil, PlatformEnvResult{Preview: input.Preview, Environment: environment, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformContextListInput struct {
	platformAliasInput
}

type PlatformContextListResult struct {
	Preview  bool                         `json:"preview"`
	Contexts []eruncommon.PlatformContext `json:"contexts,omitempty"`
	Trace    []string                     `json:"trace,omitempty"`
}

func platformContextListTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformContextListInput) (*mcp.CallToolResult, PlatformContextListResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformContextListInput) (*mcp.CallToolResult, PlatformContextListResult, error) {
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_context_list"
		contexts, err := eruncommon.RunPlatformListContexts(ctx, runtime.Store, input.Alias, cloudDependencies())
		if err != nil {
			return nil, PlatformContextListResult{}, err
		}
		return nil, PlatformContextListResult{Preview: input.Preview, Contexts: contexts, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformContextGetInput struct {
	platformAliasInput
	ContextID string `json:"contextId" jsonschema:"cloud context id to fetch"`
}

type PlatformContextResult struct {
	Preview bool                       `json:"preview"`
	Context eruncommon.PlatformContext `json:"context,omitempty"`
	Trace   []string                   `json:"trace,omitempty"`
}

func platformContextGetTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformContextGetInput) (*mcp.CallToolResult, PlatformContextResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformContextGetInput) (*mcp.CallToolResult, PlatformContextResult, error) {
		if strings.TrimSpace(input.ContextID) == "" {
			return nil, PlatformContextResult{}, fmt.Errorf("contextId is required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_context_get"
		cloudContext, err := eruncommon.RunPlatformGetContext(ctx, runtime.Store, input.Alias, input.ContextID, cloudDependencies())
		if err != nil {
			return nil, PlatformContextResult{}, err
		}
		return nil, PlatformContextResult{Preview: input.Preview, Context: cloudContext, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformContextCreateInput struct {
	platformAliasInput
	Name               string `json:"name" jsonschema:"kubernetes context name to create"`
	CloudProviderAlias string `json:"cloudProviderAlias" jsonschema:"cloud provider alias (on the tenant's own account) to bootstrap with"`
	Region             string `json:"region" jsonschema:"cloud region for the context"`
	InstanceType       string `json:"instanceType,omitempty" jsonschema:"instance type for the context's VM"`
	DiskType           string `json:"diskType,omitempty" jsonschema:"root disk type"`
	DiskSizeGB         int    `json:"diskSizeGb,omitempty" jsonschema:"root disk size in GB"`
	// PlanOnly is the platform's own server-side preview (it still reaches the
	// platform and resolves a real plan); Preview is this tool's local one and
	// skips the network call entirely. The two are independent.
	PlanOnly bool `json:"planOnly,omitempty" jsonschema:"ask the platform to resolve and return the bootstrap plan without creating anything, still a real API call unlike preview"`
}

type PlatformContextCreateResult struct {
	Preview bool                                   `json:"preview"`
	Result  eruncommon.PlatformCreateContextResult `json:"result,omitempty"`
	Trace   []string                               `json:"trace,omitempty"`
}

func platformContextCreateTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformContextCreateInput) (*mcp.CallToolResult, PlatformContextCreateResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformContextCreateInput) (*mcp.CallToolResult, PlatformContextCreateResult, error) {
		if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.CloudProviderAlias) == "" || strings.TrimSpace(input.Region) == "" {
			return nil, PlatformContextCreateResult{}, fmt.Errorf("name, cloudProviderAlias, and region are required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_context_create"
		result, err := eruncommon.RunPlatformCreateContext(ctx, runtime.Store, input.Alias, eruncommon.PlatformCreateContextParams{
			Name: input.Name, CloudProviderAlias: input.CloudProviderAlias, Region: input.Region,
			InstanceType: input.InstanceType, DiskType: input.DiskType, DiskSizeGB: input.DiskSizeGB, Preview: input.PlanOnly,
		}, cloudDependencies())
		if err != nil {
			return nil, PlatformContextCreateResult{}, err
		}
		return nil, PlatformContextCreateResult{Preview: input.Preview, Result: result, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type PlatformProvisionInput struct {
	platformAliasInput
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
	Preview bool                               `json:"preview"`
	Result  eruncommon.PlatformProvisionResult `json:"result,omitempty"`
	Trace   []string                           `json:"trace,omitempty"`
}

func platformProvisionTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PlatformProvisionInput) (*mcp.CallToolResult, PlatformProvisionResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PlatformProvisionInput) (*mcp.CallToolResult, PlatformProvisionResult, error) {
		if strings.TrimSpace(input.EnvName) == "" || strings.TrimSpace(input.EnvType) == "" {
			return nil, PlatformProvisionResult{}, fmt.Errorf("envName and envType are required")
		}
		params := eruncommon.PlatformProvisionParams{
			Environment:       eruncommon.PlatformProvisionEnvironment{Name: input.EnvName, Type: input.EnvType},
			KubernetesContext: input.KubernetesContext,
		}
		if strings.TrimSpace(input.ContextName) != "" {
			params.Context = &eruncommon.PlatformProvisionContext{
				Name: input.ContextName, CloudProviderAlias: input.ContextAlias, Region: input.ContextRegion,
				InstanceType: input.ContextInstanceType, DiskType: input.ContextDiskType, DiskSizeGB: input.ContextDiskSizeGB,
			}
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "platform_provision"
		result, err := eruncommon.RunPlatformProvision(ctx, runtime.Store, input.Alias, params, cloudDependencies())
		if err != nil {
			return nil, PlatformProvisionResult{}, err
		}
		return nil, PlatformProvisionResult{Preview: input.Preview, Result: result, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}
