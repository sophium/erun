package erunmcp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-mcp/tools/cloud"
	"github.com/sophium/erun/erun-mcp/tools/kubecontext"
	"github.com/sophium/erun/erun-mcp/tools/platform"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

func registerCloudTools(reg shared.ToolRegistrar, runtime shared.RuntimeConfig) {
	shared.AddTool(reg, &mcp.Tool{
		Name:        "cloud_list",
		Description: "List configured root-level cloud provider aliases and token status",
	}, cloud.CloudListTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "cloud_init_aws",
		Description: "Initialize an AWS SSO cloud provider alias in root ERun config, with preview support",
	}, cloud.CloudInitAWSTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "cloud_init_cloudflare",
		Description: "Initialize a Cloudflare cloud provider alias from a delegated API token (Zone + DNS edit, plus any other scopes the operator will use such as Cloudflare Pages for static sites). The token is verified against the Cloudflare API and held in a local secret store referenced from erun config, never written into erun-config.yaml; environments that attach the alias receive it as CLOUDFLARE_API_TOKEN. Supports preview.",
	}, cloud.CloudInitCloudflareTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "cloud_init_erun",
		Description: "Initialize a hosted erun platform cloud provider alias: discovers the platform's own config (OIDC issuer, CLI client id) from its unauthenticated GET /v1/platform endpoint and saves the alias — no instance's name is hardcoded. Call cloud_login afterward to sign in (Device Authorization Grant, falling back to Authorization Code + PKCE). Supports preview.",
	}, cloud.CloudInitERunTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "cloud_login",
		Description: "Login to a configured cloud provider alias, with preview support. For an erun-hosted alias, flow selects the OIDC grant: device, authcode, or auto (the default) which prefers the device grant and falls back to authorization code + PKCE when it cannot complete.",
	}, cloud.CloudLoginTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "cloud_oidc",
		Description: "Refresh the OIDC issuer for a configured cloud provider alias, with preview support",
	}, cloud.CloudOIDCTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "cloud_set",
		Description: "Set the cloud provider alias for a tenant environment, with preview support",
	}, cloud.CloudSetTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name: "cloud_inject_aws_credentials",
		Description: "Write temporary AWS credentials into the runtime pod's ~/.aws/credentials under the erun-host profile, " +
			"replacing that profile in place. The credential values are tool arguments, so DO NOT call this from anything " +
			"that records its arguments — an agent transcript, a session log, an audit trail. It exists for the desktop's " +
			"credential refresher, which holds the values in memory. To refresh an environment's host credentials from a " +
			"script, an agent, or a terminal, run `erun cloud refresh <tenant> <environment>` instead: it reads the " +
			"operator's own profile and streams the secret to the pod on stdin, so nothing sensitive passes through the caller.",
	}, shared.CloudInjectAWSCredentialsTool())
	shared.AddTool(reg, &mcp.Tool{
		Name:        "cloud_clear_aws_credentials",
		Description: "Remove the erun-host profile from the runtime pod's ~/.aws/credentials",
	}, shared.CloudClearAWSCredentialsTool())
}

func registerContextTools(reg shared.ToolRegistrar, runtime shared.RuntimeConfig) {
	shared.AddTool(reg, &mcp.Tool{
		Name:        "context_list",
		Description: "List managed ERun cloud Kubernetes contexts",
	}, kubecontext.ContextListTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "context_init",
		Description: "Initialize a managed cloud k3s Kubernetes context, with preview support",
	}, kubecontext.ContextInitTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "context_stop",
		Description: "Stop a managed ERun cloud Kubernetes context, with preview support",
	}, kubecontext.ContextStopTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "context_start",
		Description: "Start a managed ERun cloud Kubernetes context, with preview support",
	}, kubecontext.ContextStartTool(runtime))
}

func registerPlatformTools(reg shared.ToolRegistrar, runtime shared.RuntimeConfig) {
	shared.AddTool(reg, &mcp.Tool{
		Name:        "platform_whoami",
		Description: "Resolve the caller's identity against a hosted erun platform (erun-backend-api) over the erun-type cloud alias erun cloud init erun / erun cloud login set up. Supports preview.",
	}, platform.PlatformWhoamiTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "platform_version",
		Description: "Report the build actually serving a hosted erun platform's own API (erun-backend-api) over GET /v1/platform. Unauthenticated, so it still answers with an expired or missing access token -- useful for telling a stale plane apart from an unreachable one or an authorization failure on some other route. Compare the reported version against the version a route or feature was added in to tell \"merged but not deployed\" apart from a real bug. Supports preview.",
	}, platform.PlatformVersionTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "platform_tenant_create",
		Description: "Register a new tenant on the erun platform. Requires an operations-tenant caller. A real, immediate write, not a preview, unless preview is set.",
	}, platform.PlatformTenantCreateTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "platform_tenant_list",
		Description: "List tenants visible to the caller on the erun platform: every tenant for an operations-tenant caller, or just the caller's own tenant otherwise. Supports preview.",
	}, platform.PlatformTenantListTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "platform_tenant_repair-org-mapping",
		Description: "Repair a tenant already stuck with an unresolvable (issuer, org) mapping -- one that lists but that no token can ever authenticate into, such as a tenant created with no orgFieldValue before platform_tenant_create started refusing that. Converts issuer to org-scoped (if not already) and sets tenantId's own org value. Requires an operations-tenant caller. There is no tenant delete on this platform, so this is the only way back short of direct database access. A real, immediate write, not a preview, unless preview is set.",
	}, platform.PlatformTenantRepairOrgMappingTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "platform_identity_org_create",
		Description: "Create an organization on the erun platform's own identity provider. Requires an operations-tenant caller. An org-scoped tenant needs an org of its own before platform_tenant_create's orgFieldValue can produce a mapping any token will ever resolve to -- this is how a caller obtains that value. A real, immediate write, not a preview, unless preview is set.",
	}, platform.PlatformIdentityOrgCreateTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "platform_user_enroll",
		Description: "Enroll a user in a tenant on the erun platform. tenantId targets another tenant and is honored only for an operations-tenant caller. Supports preview.",
	}, platform.PlatformUserEnrollTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "platform_user_grant-role",
		Description: "Grant a role to a user already enrolled in the caller's tenant. This is the post-enrollment grant platform_user_enroll cannot perform: enrolling an identity that is already enrolled is a no-op that leaves its roles untouched. Find the role id in the tenant's role list; the role whose permissions cover what the user needs is the one to name. Requires a caller whose own role includes the grant route. A real, immediate write, not a preview, unless preview is set.",
	}, platform.PlatformUserGrantRoleTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "platform_user_list",
		Description: "List a tenant's users on the erun platform. tenantId targets another tenant and is honored only for an operations-tenant caller. Supports preview.",
	}, platform.PlatformUserListTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "platform_env_list",
		Description: "List the caller's tenant's hosted environments on the erun platform. Supports preview.",
	}, platform.PlatformEnvListTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "platform_env_get",
		Description: "Fetch one hosted environment by id from the erun platform. Supports preview.",
	}, platform.PlatformEnvGetTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "platform_env_register",
		Description: "Register a hosted environment on the erun platform. A real, immediate write, not a preview, unless preview is set. For a runtime environment with runtimeVersion set and a deploy executor configured on the platform, this also starts a server-side deploy: poll platform_env_get to watch status move registered -> provisioning -> running/failed.",
	}, platform.PlatformEnvRegisterTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "platform_env_deploy",
		Description: "Start a server-side deploy of an already-registered environment on the erun platform. Fails with a conflict if a deploy is already in progress. Supports preview.",
	}, platform.PlatformEnvDeployTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "platform_env_stop",
		Description: "Scale a hosted environment's runtime to zero on the erun platform, the server-side equivalent of `erun stop`. Supports preview.",
	}, platform.PlatformEnvStopTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "platform_env_delete",
		Description: "Start deleting a hosted environment and tearing down its remote namespace on the erun platform, the server-side equivalent of `erun delete`. Not recoverable. The teardown itself runs in the background — a namespace stuck on an unsatisfiable finalizer can take a while, so this returns as soon as the delete is accepted, with the environment's status \"deleting\". Call platform_env_get to watch it converge to gone (not found) or \"deletion-blocked\" (its deleteError names why); calling delete again against a blocked or still-deleting environment retries it. This call never prompts: pass confirm=true to actually delete. Supports preview (confirm is ignored when preview is true).",
	}, platform.PlatformEnvDeleteTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "platform_context_list",
		Description: "List the caller's tenant's cloud contexts (managed clusters) on the erun platform. Supports preview.",
	}, platform.PlatformContextListTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "platform_context_get",
		Description: "Fetch one cloud context by id from the erun platform. Supports preview.",
	}, platform.PlatformContextGetTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "platform_context_create",
		Description: "Bootstrap a cloud context (managed cluster) on the erun platform: without planOnly this launches a real cloud VM and provisions k3s on it, billing the tenant's cloud account until stopped. planOnly asks the platform to resolve and return the bootstrap plan without creating anything — a real API call, distinct from preview, which skips the network call entirely.",
	}, platform.PlatformContextCreateTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "platform_provision",
		Description: "Resolve and return the ordered plan for provisioning a hosted environment on the erun platform — tenant, quota, context bootstrap or reuse, namespace, register, deploy — without executing any of it or writing to the database. Pass either contextName (with contextAlias and contextRegion) to bootstrap a new cluster, or kubernetesContext to reuse an existing one.",
	}, platform.PlatformProvisionTool(runtime))
}
