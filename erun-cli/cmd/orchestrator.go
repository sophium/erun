package cmd

import (
	"fmt"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

// newOrchestratorCmd groups the CLI's writers for host-side orchestrator
// definitions -- the config-file counterpart to the desktop's Edit
// orchestrator dialog, so config.yaml is not the only way to set a field the
// desktop already lets an operator set.
func newOrchestratorCmd(store common.OrchestratorRoleStore) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "orchestrator",
		Short: "Manage host-side AI orchestrator definitions",
	}
	cmd.AddCommand(newOrchestratorSetRoleCmd(store))
	cmd.AddCommand(newOrchestratorSetAliasCmd(store))
	return cmd
}

func newOrchestratorSetAliasCmd(store common.OrchestratorAliasStore) *cobra.Command {
	var alias string
	cmd := &cobra.Command{
		Use:   "set-alias ORCHESTRATOR_ID",
		Short: "Set the erun platform alias an orchestrator acts as",
		Long: "Set which erun platform alias this host-side orchestrator declares as its own -- " +
			"the orchestrator-scoped counterpart of a tenant's own cloud alias, stored on the " +
			"orchestrator's entry in config.yaml and read back by `erun list`. The alias must " +
			"already be configured on this host: `erun cloud init erun --api-url <url>` adds " +
			"one, and `erun list` shows the aliases this machine has. The command refuses one " +
			"it cannot resolve rather than recording a declaration nothing could honour, and it " +
			"selects among aliases this host already has -- it never creates or signs in to " +
			"one. Pass \"" + common.OrchestratorAliasNone + "\" to declare no alias of its own again, " +
			"which is the default and leaves the orchestrator following this machine's own alias.",
		Example:      "  erun orchestrator set-alias my-orchestrator --alias erun+erunpaas.com@erun",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			parsedAlias, err := common.ParseOrchestratorAliasFlag(alias)
			if err != nil {
				return err
			}
			return runOrchestratorSetAliasCommand(commandContext(cmd), store, common.SetOrchestratorAliasParams{
				OrchestratorID: args[0],
				Alias:          parsedAlias,
			})
		},
	}
	cmd.Flags().StringVar(&alias, "alias", "", fmt.Sprintf("erun platform alias to act as, or %q to declare none", common.OrchestratorAliasNone))
	if err := cmd.MarkFlagRequired("alias"); err != nil {
		panic(err)
	}
	addDryRunFlag(cmd)
	return cmd
}

func runOrchestratorSetAliasCommand(ctx common.Context, store common.OrchestratorAliasStore, params common.SetOrchestratorAliasParams) error {
	if _, err := common.SetOrchestratorAlias(ctx, store, params); err != nil {
		return err
	}
	var err error
	if ctx.DryRun {
		_, err = fmt.Fprintln(ctx.Stdout, "Dry run: orchestrator platform alias update planned.")
		return err
	}
	alias := common.OrchestratorAliasNone
	if params.Alias != "" {
		alias = params.Alias
	}
	_, err = fmt.Fprintf(ctx.Stdout, "Set platform alias %s for orchestrator %s\n", alias, params.OrchestratorID)
	return err
}

func newOrchestratorSetRoleCmd(store common.OrchestratorRoleStore) *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use:   "set-role ORCHESTRATOR_ID TENANT ENVIRONMENT",
		Short: "Set the role an orchestrator uses one of its linked environments for",
		Long: "Set what a host-side orchestrator uses a linked environment for: a code " +
			"environment writes code and iterates fast; a build environment checks out " +
			"pushed branches, runs the gates, and cuts releases; the runtime role means the " +
			"orchestrator operates the environment directly -- deploy, pin, observe -- with no " +
			"worktree to review and no in-pod agent to delegate to, which is the only role -- " +
			"including undeclared -- a runtime-type environment may take; a host environment " +
			"takes any role except that one, having no pod for those to act on. Pass \"" + common.OrchestratorEnvRoleNone +
			"\" to declare it undeclared again; refused for a runtime-type environment, the same " +
			"way code or build is. The role is re-checked against the linked environment's type " +
			"every time, so this refuses the same pairings the desktop's link dialog would. The " +
			"environment must already be linked to the orchestrator -- see `erun list`.",
		Example:      "  erun orchestrator set-role my-orchestrator my-tenant prod --role build",
		Args:         cobra.ExactArgs(3),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			parsedRole, err := common.ParseOrchestratorEnvRoleFlag(role)
			if err != nil {
				return err
			}
			return runOrchestratorSetRoleCommand(commandContext(cmd), store, common.SetOrchestratorEnvRoleParams{
				OrchestratorID: args[0],
				Tenant:         args[1],
				Environment:    args[2],
				Role:           parsedRole,
			})
		},
	}
	cmd.Flags().StringVar(&role, "role", "", fmt.Sprintf("Role to set: %q, %q, %q, or %q for undeclared",
		common.OrchestratorEnvRoleCode, common.OrchestratorEnvRoleBuild, common.OrchestratorEnvRoleRuntime, common.OrchestratorEnvRoleNone))
	if err := cmd.MarkFlagRequired("role"); err != nil {
		panic(err)
	}
	addDryRunFlag(cmd)
	return cmd
}

func runOrchestratorSetRoleCommand(ctx common.Context, store common.OrchestratorRoleStore, params common.SetOrchestratorEnvRoleParams) error {
	if _, err := common.SetOrchestratorEnvRole(ctx, store, params); err != nil {
		return err
	}
	var err error
	if ctx.DryRun {
		_, err = fmt.Fprintln(ctx.Stdout, "Dry run: orchestrator environment role update planned.")
		return err
	}
	role := "undeclared"
	if params.Role != "" {
		role = string(params.Role)
	}
	_, err = fmt.Fprintf(ctx.Stdout, "Set role %s for %s/%s on orchestrator %s\n",
		role, params.Tenant, params.Environment, params.OrchestratorID)
	return err
}
