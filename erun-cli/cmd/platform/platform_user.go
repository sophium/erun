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

func newPlatformUserCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	return shared.NewCommandGroup(
		"user",
		"Manage users on the erun platform",
		newPlatformUserEnrollCmd(store, alias, deps),
		newPlatformUserGrantRoleCmd(store, alias, deps),
		newPlatformUserListCmd(store, alias, deps),
	)
}

func newPlatformUserGrantRoleCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	var params platformclient.PlatformGrantUserRoleParams
	cmd := &cobra.Command{
		Use:   "grant-role",
		Short: "Grant a role to an already-enrolled user on the erun platform",
		Long: "Grant one role to a user who is already enrolled in the caller's tenant.\n\n" +
			"This is the post-enrollment grant `erun platform user enroll` cannot perform: " +
			"enrolling an identity that is already enrolled is a no-op that leaves the existing " +
			"user's roles untouched, so re-running enroll with --role-id does not elevate anyone. " +
			"List the tenant's roles and their permissions with the platform's `GET /v1/roles` to " +
			"find the id of the role whose permissions cover what the user needs.\n\n" +
			"Granting is permission-gated: the caller must already hold a role that includes " +
			"POST /v1/users/{user_id}/roles.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun platform user grant-role --user-id 019a7fa5-c2c0-7c55-bc70-714873a71f60 --role-id 019a7fa5-c2c0-7c55-bc70-714873a71f70",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := shared.CommandContext(cmd)
			if err := platform.RunPlatformGrantUserRole(ctx, store, *alias, params, deps); err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform user role grant planned.")
				return err
			}
			if ctx.Output != core.OutputJSON {
				_, err := fmt.Fprintf(ctx.Stdout, "granted role %s to user %s\n", params.RoleID, params.UserID)
				return err
			}
			return ctx.WriteResult(map[string]string{"userId": params.UserID, "roleId": params.RoleID})
		},
	}
	cmd.Flags().StringVar(&params.UserID, "user-id", "", "User id to grant the role to")
	cmd.Flags().StringVar(&params.RoleID, "role-id", "", "Role id to grant")
	_ = cmd.MarkFlagRequired("user-id")
	_ = cmd.MarkFlagRequired("role-id")
	shared.AddDryRunFlag(cmd)
	return cmd
}

func newPlatformUserEnrollCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	var params platformclient.PlatformCreateUserParams
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
			ctx := shared.CommandContext(cmd)
			user, err := platform.RunPlatformCreateUser(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform user enrollment planned.")
				return err
			}
			if ctx.Output != core.OutputJSON {
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
	shared.AddDryRunFlag(cmd)
	return cmd
}

func newPlatformUserListCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	var params platformclient.PlatformListUsersParams
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List a tenant's users on the erun platform",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun platform user list\n  erun platform user list --tenant-id 018f...  # operations-tenant callers only",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := shared.CommandContext(cmd)
			users, err := platform.RunPlatformListUsers(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform user list planned.")
				return err
			}
			if ctx.Output != core.OutputJSON {
				if err := writePlatformUserList(ctx, users); err != nil {
					return err
				}
			}
			return ctx.WriteResult(users)
		},
	}
	cmd.Flags().StringVar(&params.TenantID, "tenant-id", "", "Target tenant id (operations-tenant callers only; defaults to the caller's own tenant)")
	shared.AddDryRunFlag(cmd)
	return cmd
}

func writePlatformUserList(ctx core.Context, users []platformclient.PlatformUser) error {
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
