package cmd

import (
	"fmt"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

// newPlatformCmd builds `erun platform`, the CLI's client for a hosted erun
// platform's own control-plane API (erun-backend-api), authenticating with
// the `erun`-type cloud alias `erun cloud init erun` / `erun cloud login`
// set up. It gives an operator or agent a native way to exercise a deployed
// control plane without a browser-obtained token.
func newPlatformCmd(store common.CloudReadStore, promptRunner PromptRunner, deps common.CloudDependencies) *cobra.Command {
	var alias string
	cmd := newCommandGroup(
		"platform",
		"Operate a hosted erun platform's control-plane API",
		newPlatformWhoamiCmd(store, &alias, deps),
		newPlatformTenantCmd(store, &alias, deps),
		newPlatformIdentityCmd(store, &alias, deps),
		newPlatformUserCmd(store, &alias, deps),
		newPlatformEnvCmd(store, &alias, promptRunner, deps),
		newPlatformContextCmd(store, &alias, deps),
		newPlatformProvisionCmd(store, &alias, deps),
	)
	cmd.PersistentFlags().StringVar(&alias, "erun-alias", "", "erun platform cloud alias to target (defaults to the sole configured erun-type alias)")
	return cmd
}

func newPlatformWhoamiCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "whoami",
		Short:        "Show the caller's resolved identity on the erun platform",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun platform whoami\n  erun platform whoami --erun-alias erun+api.acme.services.erunpaas.com",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := commandContext(cmd)
			whoami, err := common.RunPlatformWhoami(ctx, store, *alias, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform whoami planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if _, err := fmt.Fprintf(ctx.Stdout, "%s (tenant %s, user %s)\n", quotedValueOrNone(whoami.Username), whoami.TenantID, whoami.UserID); err != nil {
					return err
				}
			}
			return ctx.WriteResult(whoami)
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func newPlatformTenantCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	return newCommandGroup(
		"tenant",
		"Manage tenants on the erun platform",
		newPlatformTenantCreateCmd(store, alias, deps),
		newPlatformTenantListCmd(store, alias, deps),
		newPlatformTenantRepairOrgMappingCmd(store, alias, deps),
	)
}

func newPlatformTenantCreateCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var params common.PlatformCreateTenantParams
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Register a new tenant on the erun platform",
		Long: "Register a new tenant on the erun platform.\n\n" +
			"Requires the caller to be signed in as an operations tenant. Writes a new row to the " +
			"platform's own database and maps the given OIDC issuer to it — a real, immediate " +
			"mutation of shared control-plane state, not a preview.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun platform tenant create --name acme --issuer https://acme.example.com --display-name Acme",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := commandContext(cmd)
			tenant, err := common.RunPlatformCreateTenant(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform tenant creation planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if _, err := fmt.Fprintf(ctx.Stdout, "created tenant %s (%s)\n", tenant.Name, tenant.TenantID); err != nil {
					return err
				}
			}
			return ctx.WriteResult(tenant)
		},
	}
	cmd.Flags().StringVar(&params.Name, "name", "", "Tenant name (hyphen-free; forms the <tenant>-<env> namespace)")
	cmd.Flags().StringVar(&params.Type, "type", "COMPANY", "Tenant type: COMPANY or OPERATIONS")
	cmd.Flags().StringVar(&params.Issuer, "issuer", "", "OIDC issuer that resolves tokens to this tenant")
	cmd.Flags().StringVar(&params.OrgFieldKey, "org-field-key", "", "Claim name that carries the org for a shared (multi-tenant) issuer")
	cmd.Flags().StringVar(&params.OrgFieldValue, "org-field-value", "", "Claim value identifying this tenant under a shared issuer")
	cmd.Flags().StringVar(&params.DisplayName, "display-name", "", "Human-readable label for the tenant/issuer mapping (defaults to the issuer)")
	addDryRunFlag(cmd)
	return cmd
}

func newPlatformTenantListCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List tenants visible to the caller",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := commandContext(cmd)
			tenants, err := common.RunPlatformListTenants(ctx, store, *alias, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform tenant list planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writePlatformTenantList(ctx, tenants); err != nil {
					return err
				}
			}
			return ctx.WriteResult(tenants)
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func writePlatformTenantList(ctx common.Context, tenants []common.PlatformTenant) error {
	if len(tenants) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "no tenants")
		return err
	}
	for _, tenant := range tenants {
		if _, err := fmt.Fprintf(ctx.Stdout, "  - %s (%s) type=%s%s\n", tenant.Name, tenant.TenantID, tenant.Type, tenantUnreachableSuffix(tenant)); err != nil {
			return err
		}
	}
	return nil
}

// tenantUnreachableSuffix flags a tenant whose issuer mapping no token can
// resolve through. Such a tenant lists exactly like a healthy one, so without
// this the only way to discover it is a sign-in that lands somewhere else.
// A nil Resolvable is "not computed", not "reachable", and stays silent rather
// than accusing every tenant on a read path that never asked.
func tenantUnreachableSuffix(tenant common.PlatformTenant) string {
	if tenant.Resolvable == nil || *tenant.Resolvable {
		return ""
	}
	return " UNREACHABLE (no issuer mapping any token can resolve through)"
}

// newPlatformIdentityCmd builds `erun platform identity`, the CLI's client
// for the platform's own IdP administration surface (erun-backend-api's
// /v1/identity/* routes). Today it covers exactly the operation `platform
// tenant create` depends on: creating the org an org-scoped tenant mapping
// needs. Before this, POST /v1/identity/orgs had no CLI, MCP, or desktop
// surface at all, so the documented tenant-creation flow could produce a
// tenant with no reachable org value — created, listed, and permanently
// unauthenticatable.
// newPlatformTenantRepairOrgMappingCmd fixes a tenant already stuck with an
// unresolvable (issuer, org) mapping -- created before POST /v1/tenants
// started refusing an org-scoped mapping with no org value, or left behind
// when its issuer was converted to org-scoped after it registered. There is
// no tenant delete endpoint on the platform at all, so this repair (not
// delete-and-recreate, which is not even possible) is the only way back for
// a tenant already in that state.
func newPlatformTenantRepairOrgMappingCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var params common.PlatformRepairTenantIssuerOrgMappingParams
	cmd := &cobra.Command{
		Use:   "repair-org-mapping",
		Short: "Repair a tenant's dead (issuer, org) mapping so it resolves again",
		Long: "Repair a tenant's dead (issuer, org) mapping so it resolves again.\n\n" +
			"Requires the caller to be signed in as an operations tenant. Converts issuer to " +
			"org-scoped (if it is not already) and sets --tenant-id's own org value -- the fix for a " +
			"tenant that lists but that no token can ever authenticate into, such as one " +
			"`platform tenant create` produced with no --org-field-value before it started refusing " +
			"that. There is no tenant delete on this platform, so this is the only way back short of " +
			"direct database access. A real, immediate mutation of shared control-plane state, not a preview.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun platform tenant repair-org-mapping --tenant-id 01... --issuer https://auth.example --org-field-key urn:zitadel:iam:user:resourceowner:id --org-field-value 42",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := commandContext(cmd)
			issuer, err := common.RunPlatformRepairTenantIssuerOrgMapping(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform tenant org-mapping repair planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if _, err := fmt.Fprintf(ctx.Stdout, "repaired %s: org-field-value now %s\n", issuer.Issuer, issuer.OrgFieldValue); err != nil {
					return err
				}
			}
			return ctx.WriteResult(issuer)
		},
	}
	cmd.Flags().StringVar(&params.TenantID, "tenant-id", "", "Tenant to repair (operations-tenant callers only; defaults to the caller's own tenant)")
	cmd.Flags().StringVar(&params.Issuer, "issuer", "", "OIDC issuer the tenant is mapped under")
	cmd.Flags().StringVar(&params.OrgFieldKey, "org-field-key", "", "Claim name that carries the org for this shared issuer")
	cmd.Flags().StringVar(&params.OrgFieldValue, "org-field-value", "", "Org value to set on the tenant's mapping (see platform identity org create)")
	addDryRunFlag(cmd)
	return cmd
}

func newPlatformIdentityCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	return newCommandGroup(
		"identity",
		"Administer the erun platform's own identity provider",
		newPlatformIdentityOrgCmd(store, alias, deps),
	)
}

func newPlatformIdentityOrgCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	return newCommandGroup(
		"org",
		"Manage organizations on the erun platform's own identity provider",
		newPlatformIdentityOrgCreateCmd(store, alias, deps),
	)
}

func newPlatformIdentityOrgCreateCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var params common.PlatformCreateOrgParams
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an organization on the erun platform's own identity provider",
		Long: "Create an organization on the erun platform's own identity provider.\n\n" +
			"Requires the caller to be signed in as an operations tenant. An org-scoped tenant " +
			"(one sharing an issuer with another tenant) needs an org of its own before " +
			"`platform tenant create --org-field-value` can produce a mapping any token will " +
			"ever resolve to -- this is how an operator obtains that value. A real, immediate " +
			"mutation of shared control-plane state, not a preview.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun platform identity org create --name acme",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := commandContext(cmd)
			org, err := common.RunPlatformCreateOrg(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform identity org creation planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if _, err := fmt.Fprintf(ctx.Stdout, "created org %s (%s) -- pass this id as --org-field-value\n", org.Name, org.ID); err != nil {
					return err
				}
			}
			return ctx.WriteResult(org)
		},
	}
	cmd.Flags().StringVar(&params.Name, "name", "", "Organization name")
	addDryRunFlag(cmd)
	return cmd
}

func newPlatformUserCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	return newCommandGroup(
		"user",
		"Manage users on the erun platform",
		newPlatformUserEnrollCmd(store, alias, deps),
		newPlatformUserListCmd(store, alias, deps),
	)
}

func newPlatformUserEnrollCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var params common.PlatformCreateUserParams
	cmd := &cobra.Command{
		Use:   "enroll",
		Short: "Enroll a user in a tenant on the erun platform",
		Long: "Enroll a user in a tenant on the erun platform.\n\n" +
			"Writes a new user row, immediately. Pass --issuer and --subject to link the external " +
			"identity the user signs in with; without them the user cannot sign in until an identity " +
			"is linked. If that identity is already enrolled in the target tenant, this is a no-op: " +
			"it reports the existing user rather than failing on a username collision. --tenant-id " +
			"targets another tenant and is honored only for an operations-tenant caller. --role-id " +
			"(repeatable) names the roles to grant instead of the platform's default, which is how a " +
			"tenant's administrator is enrolled directly rather than a member nobody inside the " +
			"tenant can elevate.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun platform user enroll --username jane --issuer https://acme.example.com --subject jane@acme.com",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := commandContext(cmd)
			user, err := common.RunPlatformCreateUser(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform user enrollment planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				message := fmt.Sprintf("enrolled user %s (%s) in tenant %s\n", user.Username, user.UserID, user.TenantID)
				if user.AlreadyEnrolled {
					message = fmt.Sprintf("this identity is already enrolled, as %s (%s) in tenant %s\n", user.Username, user.UserID, user.TenantID)
				}
				if _, err := fmt.Fprint(ctx.Stdout, message); err != nil {
					return err
				}
			}
			return ctx.WriteResult(user)
		},
	}
	cmd.Flags().StringVar(&params.Username, "username", "", "Username to enroll")
	cmd.Flags().StringVar(&params.Issuer, "issuer", "", "OIDC issuer of the external identity to link")
	cmd.Flags().StringVar(&params.Subject, "subject", "", "OIDC subject of the external identity to link")
	cmd.Flags().StringVar(&params.TenantID, "tenant-id", "", "Target tenant id (operations-tenant callers only; defaults to the caller's own tenant)")
	cmd.Flags().StringArrayVar(&params.RoleIDs, "role-id", nil, "Role id to grant, repeatable (defaults to the platform's own default role for this enrollment)")
	addDryRunFlag(cmd)
	return cmd
}

func newPlatformUserListCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var params common.PlatformListUsersParams
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List a tenant's users on the erun platform",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun platform user list\n  erun platform user list --tenant-id 018f...  # operations-tenant callers only",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := commandContext(cmd)
			users, err := common.RunPlatformListUsers(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform user list planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writePlatformUserList(ctx, users); err != nil {
					return err
				}
			}
			return ctx.WriteResult(users)
		},
	}
	cmd.Flags().StringVar(&params.TenantID, "tenant-id", "", "Target tenant id (operations-tenant callers only; defaults to the caller's own tenant)")
	addDryRunFlag(cmd)
	return cmd
}

func writePlatformUserList(ctx common.Context, users []common.PlatformUser) error {
	if len(users) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "no users")
		return err
	}
	for _, user := range users {
		if _, err := fmt.Fprintf(ctx.Stdout, "  - %s (%s)\n", user.Username, user.UserID); err != nil {
			return err
		}
	}
	return nil
}

func newPlatformEnvCmd(store common.CloudReadStore, alias *string, promptRunner PromptRunner, deps common.CloudDependencies) *cobra.Command {
	return newCommandGroup(
		"env",
		"Manage hosted environments on the erun platform",
		newPlatformEnvListCmd(store, alias, deps),
		newPlatformEnvGetCmd(store, alias, deps),
		newPlatformEnvRegisterCmd(store, alias, deps),
		newPlatformEnvDeployCmd(store, alias, deps),
		newPlatformEnvStopCmd(store, alias, deps),
		newPlatformEnvDeleteCmd(store, alias, promptRunner, deps),
	)
}

func newPlatformEnvListCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List the caller's tenant's hosted environments",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := commandContext(cmd)
			environments, err := common.RunPlatformListEnvironments(ctx, store, *alias, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform environment list planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writePlatformEnvironmentList(ctx, environments); err != nil {
					return err
				}
			}
			return ctx.WriteResult(environments)
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func writePlatformEnvironmentList(ctx common.Context, environments []common.PlatformEnvironment) error {
	if len(environments) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "no environments")
		return err
	}
	for _, environment := range environments {
		if err := writePlatformEnvironmentLine(ctx, environment); err != nil {
			return err
		}
	}
	return nil
}

func writePlatformEnvironmentLine(ctx common.Context, environment common.PlatformEnvironment) error {
	line := fmt.Sprintf("  - %s (%s) type=%s status=%s", environment.Name, environment.EnvironmentID, environment.Type, environment.Status)
	if strings.TrimSpace(environment.RuntimeVersion) != "" {
		line += " runtime-version=" + environment.RuntimeVersion
	}
	if strings.TrimSpace(environment.ProvisionError) != "" {
		line += " provision-error=" + quotedValueOrNone(environment.ProvisionError)
	}
	if strings.TrimSpace(environment.DeleteError) != "" {
		line += " delete-error=" + quotedValueOrNone(environment.DeleteError)
	}
	_, err := fmt.Fprintln(ctx.Stdout, line)
	return err
}

func newPlatformEnvGetCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "get ENVIRONMENT_ID",
		Short:        "Fetch one hosted environment by id",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			environment, err := common.RunPlatformGetEnvironment(ctx, store, *alias, args[0], deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform environment lookup planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writePlatformEnvironmentLine(ctx, environment); err != nil {
					return err
				}
			}
			return ctx.WriteResult(environment)
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func newPlatformEnvRegisterCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var params common.PlatformCreateEnvironmentParams
	cmd := &cobra.Command{
		Use:   "register",
		Short: "Register a hosted environment on the erun platform",
		Long: "Register a hosted environment on the erun platform.\n\n" +
			"Writes a new environment row, immediately, and — for a runtime environment with " +
			"--runtime-version set and a deploy executor configured on the platform — also starts " +
			"a server-side deploy: the response's status moves registered -> provisioning -> " +
			"running/failed, so poll `erun platform env get` to watch it land.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun platform env register --name prod --type runtime --context-id 018f... --runtime-version 1.4.2",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := commandContext(cmd)
			environment, err := common.RunPlatformRegisterEnvironment(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform environment registration planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writePlatformEnvironmentLine(ctx, environment); err != nil {
					return err
				}
			}
			return ctx.WriteResult(environment)
		},
	}
	cmd.Flags().StringVar(&params.Name, "name", "", "Environment name (DNS-1123 label; forms the <tenant>-<env> namespace)")
	cmd.Flags().StringVar(&params.Type, "type", "", "Environment type: runtime, remote-agent, or local-agent")
	cmd.Flags().StringVar(&params.ContextID, "context-id", "", "Cloud context to deploy into (see `erun platform context list`)")
	cmd.Flags().StringVar(&params.KubernetesContext, "kubernetes-context", "", "Kubernetes context name to deploy into, if not using --context-id")
	cmd.Flags().StringVar(&params.RuntimeVersion, "runtime-version", "", "Published erun runtime version to deploy (runtime environments only)")
	addDryRunFlag(cmd)
	return cmd
}

func newPlatformEnvDeployCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var params common.PlatformDeployEnvironmentParams
	cmd := &cobra.Command{
		Use:   "deploy ENVIRONMENT_ID",
		Short: "Start a server-side deploy of an already-registered environment",
		Long: "Start a server-side deploy of an already-registered environment.\n\n" +
			"Starts the platform's own deploy executor, which helm-installs a published erun " +
			"runtime version into the environment's namespace. Fails with a conflict if a deploy " +
			"is already in progress for this environment. --version re-deploys at an explicit " +
			"version; omitted, the environment's own pinned runtime version is deployed.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		Example:      "  erun platform env deploy 018f...\n  erun platform env deploy 018f... --version 1.4.2",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			environment, err := common.RunPlatformDeployEnvironment(ctx, store, *alias, args[0], params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform environment deploy planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writePlatformEnvironmentLine(ctx, environment); err != nil {
					return err
				}
			}
			return ctx.WriteResult(environment)
		},
	}
	cmd.Flags().StringVar(&params.Version, "version", "", "Published version to deploy (defaults to the environment's pinned runtime version)")
	addDryRunFlag(cmd)
	return cmd
}

func newPlatformEnvStopCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop ENVIRONMENT_ID",
		Short: "Scale a hosted environment's runtime to zero",
		Long: "Scale a hosted environment's runtime to zero.\n\n" +
			"The server-side equivalent of `erun stop`: the platform scales the environment's " +
			"runtime Deployment to zero. Persistent state is untouched; a later deploy or open " +
			"wakes it again.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			environment, err := common.RunPlatformStopEnvironment(ctx, store, *alias, args[0], deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform environment stop planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writePlatformEnvironmentLine(ctx, environment); err != nil {
					return err
				}
			}
			return ctx.WriteResult(environment)
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func newPlatformEnvDeleteCmd(store common.CloudReadStore, alias *string, promptRunner PromptRunner, deps common.CloudDependencies) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete ENVIRONMENT_ID",
		Short: "Start deleting a hosted environment and tearing down its remote namespace",
		Long: "Start deleting a hosted environment and tearing down its remote namespace.\n\n" +
			"The server-side equivalent of `erun delete`: the platform starts tearing down the " +
			"environment's namespace and its data, then removes the row. Not recoverable. The " +
			"teardown itself runs in the background — a namespace stuck on an unsatisfiable " +
			"finalizer can take a while, so this returns as soon as the delete is accepted, with " +
			"status \"deleting\". Run `erun platform env get` to watch it converge to gone (not " +
			"found) or \"deletion-blocked\" (naming why); a delete command against a blocked or " +
			"still-deleting environment retries it. Asks for confirmation; -y skips the prompt for " +
			"non-interactive callers.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		Example:      "  erun platform env delete 018f...\n  erun platform env delete 018f... -y",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			if !ctx.DryRun && !yes {
				confirmed, err := confirmPrompt(promptRunner, fmt.Sprintf("Delete environment %s (irreversible)", args[0]))
				if err != nil {
					return err
				}
				if !confirmed {
					return fmt.Errorf("delete cancelled")
				}
			}
			environment, err := common.RunPlatformDeleteEnvironment(ctx, store, *alias, args[0], deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform environment deletion planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writePlatformEnvironmentLine(ctx, environment); err != nil {
					return err
				}
			}
			return ctx.WriteResult(environment)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt (for non-interactive callers)")
	addDryRunFlag(cmd)
	return cmd
}

func newPlatformContextCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	return newCommandGroup(
		"context",
		"Manage the erun platform's own cloud contexts (managed clusters)",
		newPlatformContextCreateCmd(store, alias, deps),
		newPlatformContextListCmd(store, alias, deps),
		newPlatformContextGetCmd(store, alias, deps),
	)
}

func newPlatformContextCreateCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var params common.PlatformCreateContextParams
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Bootstrap or preview a cloud context (managed cluster) on the erun platform",
		Long: "Bootstrap or preview a cloud context (managed cluster) on the erun platform.\n\n" +
			"Without --preview this launches a real cloud VM and provisions k3s on it, billing the " +
			"tenant's cloud account until stopped. --preview asks the platform to resolve and " +
			"return the bootstrap plan without creating anything (a server-side check, distinct " +
			"from --dry-run, which skips the network call entirely).",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun platform context create --name prod --alias aws-main --region eu-west-2 --preview\n  erun platform context create --name prod --alias aws-main --region eu-west-2",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := commandContext(cmd)
			result, err := common.RunPlatformCreateContext(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform context creation planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writePlatformCreateContextResult(ctx, result); err != nil {
					return err
				}
			}
			return ctx.WriteResult(result)
		},
	}
	cmd.Flags().StringVar(&params.Name, "name", "", "Kubernetes context name to create")
	cmd.Flags().StringVar(&params.CloudProviderAlias, "alias", "", "Cloud provider alias (on the tenant's own account) to bootstrap with")
	cmd.Flags().StringVar(&params.Region, "region", "", "Cloud region for the context")
	cmd.Flags().StringVar(&params.InstanceType, "instance-type", "", "Instance type for the context's VM")
	cmd.Flags().StringVar(&params.DiskType, "disk-type", "", "Root disk type")
	cmd.Flags().IntVar(&params.DiskSizeGB, "disk-size-gb", 0, "Root disk size in GB")
	cmd.Flags().BoolVar(&params.Preview, "preview", false, "Resolve and return the bootstrap plan without creating anything")
	addDryRunFlag(cmd)
	return cmd
}

func writePlatformCreateContextResult(ctx common.Context, result common.PlatformCreateContextResult) error {
	for _, line := range result.Plan {
		if _, err := fmt.Fprintln(ctx.Stdout, "  "+line); err != nil {
			return err
		}
	}
	if result.Context == nil {
		return nil
	}
	_, err := fmt.Fprintf(ctx.Stdout, "created context %s (%s) status=%s\n", result.Context.Name, result.Context.ContextID, result.Context.Status)
	return err
}

func newPlatformContextListCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List the caller's tenant's cloud contexts on the erun platform",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := commandContext(cmd)
			contexts, err := common.RunPlatformListContexts(ctx, store, *alias, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform context list planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writePlatformContextList(ctx, contexts); err != nil {
					return err
				}
			}
			return ctx.WriteResult(contexts)
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func writePlatformContextList(ctx common.Context, contexts []common.PlatformContext) error {
	if len(contexts) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "no contexts")
		return err
	}
	for _, cloudContext := range contexts {
		if _, err := fmt.Fprintf(ctx.Stdout, "  - %s (%s) status=%s region=%s\n", cloudContext.Name, cloudContext.ContextID, cloudContext.Status, quotedValueOrNone(cloudContext.Region)); err != nil {
			return err
		}
	}
	return nil
}

func newPlatformContextGetCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "get CONTEXT_ID",
		Short:        "Fetch one cloud context by id",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			cloudContext, err := common.RunPlatformGetContext(ctx, store, *alias, args[0], deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform context lookup planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if _, err := fmt.Fprintf(ctx.Stdout, "  - %s (%s) status=%s region=%s\n", cloudContext.Name, cloudContext.ContextID, cloudContext.Status, quotedValueOrNone(cloudContext.Region)); err != nil {
					return err
				}
			}
			return ctx.WriteResult(cloudContext)
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func newPlatformProvisionCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var params common.PlatformProvisionParams
	cmd := &cobra.Command{
		Use:   "provision",
		Short: "Preview the ordered plan for provisioning a hosted environment",
		Long: "Preview the ordered plan for provisioning a hosted environment.\n\n" +
			"Asks the platform to resolve tenant, quota, context bootstrap (or reuse), namespace, " +
			"registration, and deploy into one ordered plan, without executing any of it or writing " +
			"to the database. Pass either --context-name (with --context-alias and --context-region) " +
			"to bootstrap a new cluster, or --kubernetes-context to reuse an existing one.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example: "  erun platform provision --env-name prod --env-type runtime --kubernetes-context prod-cluster\n" +
			"  erun platform provision --env-name prod --env-type runtime --context-name prod --context-alias aws-main --context-region eu-west-2",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := commandContext(cmd)
			result, err := common.RunPlatformProvision(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform provision preview planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writePlatformProvisionResult(ctx, result); err != nil {
					return err
				}
			}
			return ctx.WriteResult(result)
		},
	}
	cmd.Flags().StringVar(&params.Environment.Name, "env-name", "", "Environment name to provision")
	cmd.Flags().StringVar(&params.Environment.Type, "env-type", "", "Environment type: runtime, remote-agent, or local-agent")
	cmd.Flags().StringVar(&params.KubernetesContext, "kubernetes-context", "", "Reuse an existing kubernetes context instead of bootstrapping one")
	contextName, contextAlias, contextRegion := "", "", ""
	var contextInstanceType, contextDiskType string
	var contextDiskSizeGB int
	cmd.Flags().StringVar(&contextName, "context-name", "", "Name for a new cloud context to bootstrap")
	cmd.Flags().StringVar(&contextAlias, "context-alias", "", "Cloud provider alias to bootstrap the new context with")
	cmd.Flags().StringVar(&contextRegion, "context-region", "", "Cloud region for the new context")
	cmd.Flags().StringVar(&contextInstanceType, "context-instance-type", "", "Instance type for the new context's VM")
	cmd.Flags().StringVar(&contextDiskType, "context-disk-type", "", "Root disk type for the new context")
	cmd.Flags().IntVar(&contextDiskSizeGB, "context-disk-size-gb", 0, "Root disk size in GB for the new context")
	originalRunE := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(contextName) != "" {
			params.Context = &common.PlatformProvisionContext{
				Name:               contextName,
				CloudProviderAlias: contextAlias,
				Region:             contextRegion,
				InstanceType:       contextInstanceType,
				DiskType:           contextDiskType,
				DiskSizeGB:         contextDiskSizeGB,
			}
		}
		return originalRunE(cmd, args)
	}
	addDryRunFlag(cmd)
	return cmd
}

func writePlatformProvisionResult(ctx common.Context, result common.PlatformProvisionResult) error {
	for _, line := range result.Plan {
		if _, err := fmt.Fprintln(ctx.Stdout, "  "+line); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(ctx.Stdout, "quota ok: %t\n", result.QuotaOk)
	return err
}
