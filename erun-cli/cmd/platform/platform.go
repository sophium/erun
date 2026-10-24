package platform

import (
	"fmt"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/platform"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/spf13/cobra"
)

// NewPlatformCmd builds `erun platform`, the CLI's client for a hosted erun
// platform's own control-plane API (erun-backend-api), authenticating with
// the `erun`-type cloud alias `erun cloud init erun` / `erun cloud login`
// set up. It gives an operator or agent a native way to exercise a deployed
// control plane without a browser-obtained token.
func NewPlatformCmd(store config.CloudReadStore, promptRunner shared.PromptRunner, deps cloud.CloudDependencies) *cobra.Command {
	var alias string
	cmd := shared.NewCommandGroup(
		"platform",
		"Operate a hosted erun platform's control-plane API",
		newPlatformWhoamiCmd(store, &alias, deps),
		newPlatformVersionCmd(store, &alias, deps),
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

func newPlatformWhoamiCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "whoami",
		Short:        "Show the caller's resolved identity on the erun platform",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun platform whoami\n  erun platform whoami --erun-alias erun+api.acme.services.erunpaas.com",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := shared.CommandContext(cmd)
			whoami, err := platform.RunPlatformWhoami(ctx, store, *alias, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform whoami planned.")
				return err
			}
			if ctx.Output != core.OutputJSON {
				if _, err := fmt.Fprintf(ctx.Stdout, "%s (tenant %s named %q, user %s)\n", shared.QuotedValueOrNone(whoami.Username), whoami.TenantID, shared.QuotedValueOrNone(whoami.TenantName), whoami.UserID); err != nil {
					return err
				}
			}
			return ctx.WriteResult(whoami)
		},
	}
	shared.AddDryRunFlag(cmd)
	return cmd
}

func newPlatformVersionCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Report the erun platform's own version and build",
		Long: "Report the erun platform's own version and build.\n\n" +
			"Unauthenticated: it works even with an expired or missing access token, so it stays " +
			"useful for telling a stale plane apart from an unreachable one or an authorization " +
			"failure on some other route. Compare the reported version against the version a route " +
			"or feature was added in to tell \"merged but not deployed\" apart from a real bug.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun platform version\n  erun platform version --erun-alias erun+api.acme.services.erunpaas.com",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := shared.CommandContext(cmd)
			info, err := platform.RunPlatformVersion(ctx, store, *alias, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform version lookup planned.")
				return err
			}
			if ctx.Output != core.OutputJSON {
				if _, err := fmt.Fprintf(ctx.Stdout, "erun platform version %s (%s)\n", info.Version, info.APIURL); err != nil {
					return err
				}
			}
			return ctx.WriteResult(info)
		},
	}
	shared.AddDryRunFlag(cmd)
	return cmd
}

func newPlatformTenantCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	return shared.NewCommandGroup(
		"tenant",
		"Manage tenants on the erun platform",
		newPlatformTenantCreateCmd(store, alias, deps),
		newPlatformTenantListCmd(store, alias, deps),
		newPlatformTenantRepairOrgMappingCmd(store, alias, deps),
	)
}

func newPlatformTenantCreateCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	var params platformclient.PlatformCreateTenantParams
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
			ctx := shared.CommandContext(cmd)
			tenant, err := platform.RunPlatformCreateTenant(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform tenant creation planned.")
				return err
			}
			if ctx.Output != core.OutputJSON {
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
	shared.AddDryRunFlag(cmd)
	return cmd
}

func newPlatformTenantListCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List tenants visible to the caller",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := shared.CommandContext(cmd)
			tenants, err := platform.RunPlatformListTenants(ctx, store, *alias, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform tenant list planned.")
				return err
			}
			if ctx.Output != core.OutputJSON {
				if err := writePlatformTenantList(ctx, tenants); err != nil {
					return err
				}
			}
			return ctx.WriteResult(tenants)
		},
	}
	shared.AddDryRunFlag(cmd)
	return cmd
}

func writePlatformTenantList(ctx core.Context, tenants []platformclient.PlatformTenant) error {
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
func tenantUnreachableSuffix(tenant platformclient.PlatformTenant) string {
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
func newPlatformTenantRepairOrgMappingCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	var params platformclient.PlatformRepairTenantIssuerOrgMappingParams
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
			ctx := shared.CommandContext(cmd)
			issuer, err := platform.RunPlatformRepairTenantIssuerOrgMapping(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform tenant org-mapping repair planned.")
				return err
			}
			if ctx.Output != core.OutputJSON {
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
	shared.AddDryRunFlag(cmd)
	return cmd
}

func newPlatformIdentityCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	return shared.NewCommandGroup(
		"identity",
		"Administer the erun platform's own identity provider",
		newPlatformIdentityOrgCmd(store, alias, deps),
	)
}

func newPlatformIdentityOrgCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	return shared.NewCommandGroup(
		"org",
		"Manage organizations on the erun platform's own identity provider",
		newPlatformIdentityOrgCreateCmd(store, alias, deps),
	)
}

func newPlatformIdentityOrgCreateCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	var params platformclient.PlatformCreateOrgParams
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
			ctx := shared.CommandContext(cmd)
			org, err := platform.RunPlatformCreateOrg(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform identity org creation planned.")
				return err
			}
			if ctx.Output != core.OutputJSON {
				if _, err := fmt.Fprintf(ctx.Stdout, "created org %s (%s) -- pass this id as --org-field-value\n", org.Name, org.ID); err != nil {
					return err
				}
			}
			return ctx.WriteResult(org)
		},
	}
	cmd.Flags().StringVar(&params.Name, "name", "", "Organization name")
	shared.AddDryRunFlag(cmd)
	return cmd
}
