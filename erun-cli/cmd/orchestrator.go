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
	return cmd
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
			"worktree to review and no in-pod agent to delegate to, which is the only role a " +
			"runtime-type environment may take. Pass \"" + common.OrchestratorEnvRoleNone +
			"\" to declare it undeclared again. The environment must already be linked to " +
			"the orchestrator -- see `erun list`.",
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
