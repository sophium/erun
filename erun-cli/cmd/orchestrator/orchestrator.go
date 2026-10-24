package orchestrator

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/ai"
	"github.com/sophium/erun/erun-common/command/orchestrator"
	"github.com/spf13/cobra"
)

// NewOrchestratorCmd groups the CLI's writers for host-side orchestrator
// definitions -- the config-file counterpart to the desktop's Edit
// orchestrator dialog, so config.yaml is not the only way to set a field the
// desktop already lets an operator set.
func NewOrchestratorCmd(store orchestrator.OrchestratorRoleStore) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "orchestrator",
		Short: "Manage host-side AI orchestrator definitions",
	}
	cmd.AddCommand(newOrchestratorSetRoleCmd(store))
	cmd.AddCommand(newOrchestratorSetAliasCmd(store))
	cmd.AddCommand(newOrchestratorSetGatewayCmd(store))
	cmd.AddCommand(newOrchestratorSetModelCmd(store))
	cmd.AddCommand(newOrchestratorSetEffortCmd(store))
	return cmd
}

// orchestratorLaunchSettingStorage is the tail every launch-setting writer's
// help shares: where the value lives, where it is read back, and when it applies.
const orchestratorLaunchSettingStorage = "Stored on the orchestrator's entry in config.yaml and read " +
	"back by `erun list`. A running orchestrator keeps the launch it started with until it is restarted."

func newOrchestratorSetGatewayCmd(store orchestrator.OrchestratorAliasStore) *cobra.Command {
	var gateway string
	cmd := &cobra.Command{
		Use:   "set-gateway ORCHESTRATOR_ID",
		Short: "Turn an orchestrator's use of the gateway catalog on or off",
		Long: "Turn on or off whether this host-side orchestrator's Claude session routes through the " +
			"erun-level gateway catalog -- the one configured in the desktop's ERun settings, which " +
			"environments launch through too. Off is the default and leaves the orchestrator on this " +
			"machine's own Claude Code configuration. Turning it on is refused while no gateway catalog " +
			"is configured, and while the orchestrator's recorded model is one the gateway cannot drive. " +
			"A gateway launch also needs the gateway key saved in the desktop's ERun settings; without it " +
			"the orchestrator refuses to start. " + orchestratorLaunchSettingStorage,
		Example: "  erun orchestrator set-gateway my-orchestrator --gateway on\n" +
			"  erun orchestrator set-gateway my-orchestrator --gateway off",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			useGateway, err := orchestrator.ParseOrchestratorGatewayFlag(gateway)
			if err != nil {
				return err
			}
			return runOrchestratorSetGatewayCommand(shared.CommandContext(cmd), store, orchestrator.SetOrchestratorGatewayParams{
				OrchestratorID: args[0],
				UseGateway:     useGateway,
			})
		},
	}
	cmd.Flags().StringVar(&gateway, "gateway", "", `"on" to route through the gateway catalog, "off" for this machine's own Claude Code configuration`)
	if err := cmd.MarkFlagRequired("gateway"); err != nil {
		panic(err)
	}
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runOrchestratorSetGatewayCommand(ctx core.Context, store orchestrator.OrchestratorAliasStore, params orchestrator.SetOrchestratorGatewayParams) error {
	if _, err := orchestrator.SetOrchestratorGateway(ctx, store, params); err != nil {
		return err
	}
	var err error
	if ctx.DryRun {
		_, err = fmt.Fprintln(ctx.Stdout, "Dry run: orchestrator gateway update planned.")
		return err
	}
	gateway := "off"
	if params.UseGateway {
		gateway = "on"
	}
	_, err = fmt.Fprintf(ctx.Stdout, "Set gateway %s for orchestrator %s\n", gateway, params.OrchestratorID)
	return err
}

func newOrchestratorSetModelCmd(store orchestrator.OrchestratorAliasStore) *cobra.Command {
	var model string
	cmd := &cobra.Command{
		Use:   "set-model ORCHESTRATOR_ID",
		Short: "Set the model an orchestrator's Claude session launches on",
		Long: "Set which model this host-side orchestrator's Claude session and its subagents launch on. " +
			"Pass \"" + orchestrator.OrchestratorLaunchSettingDefault + "\" to clear it: the default is " +
			orchestrator.DefaultOrchestratorModel + " on this machine's own Claude Code configuration, and the " +
			"gateway catalog's default model when the orchestrator routes through the gateway (" +
			orchestrator.DefaultOrchestratorModel + " if the catalog names none). A model id is letters, digits " +
			"and . _ : / - and cannot start with -. With the gateway turned on, a model the gateway cannot " +
			"drive is refused. " + orchestratorLaunchSettingStorage,
		Example: "  erun orchestrator set-model my-orchestrator --model sonnet\n" +
			"  erun orchestrator set-model my-orchestrator --model " + orchestrator.OrchestratorLaunchSettingDefault,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			parsedModel, err := orchestrator.ParseOrchestratorModelFlag(model)
			if err != nil {
				return err
			}
			return runOrchestratorSetModelCommand(shared.CommandContext(cmd), store, orchestrator.SetOrchestratorModelParams{
				OrchestratorID: args[0],
				Model:          parsedModel,
			})
		},
	}
	cmd.Flags().StringVar(&model, "model", "", fmt.Sprintf("Model id to launch on, or %q to clear it", orchestrator.OrchestratorLaunchSettingDefault))
	if err := cmd.MarkFlagRequired("model"); err != nil {
		panic(err)
	}
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runOrchestratorSetModelCommand(ctx core.Context, store orchestrator.OrchestratorAliasStore, params orchestrator.SetOrchestratorModelParams) error {
	if _, err := orchestrator.SetOrchestratorModel(ctx, store, params); err != nil {
		return err
	}
	var err error
	if ctx.DryRun {
		_, err = fmt.Fprintln(ctx.Stdout, "Dry run: orchestrator model update planned.")
		return err
	}
	_, err = fmt.Fprintf(ctx.Stdout, "Set model %s for orchestrator %s\n", orchestratorLaunchSettingLabel(params.Model), params.OrchestratorID)
	return err
}

func newOrchestratorSetEffortCmd(store orchestrator.OrchestratorAliasStore) *cobra.Command {
	var effort string
	levels := strings.Join(ai.ClaudeEffortLevels(), ", ")
	cmd := &cobra.Command{
		Use:   "set-effort ORCHESTRATOR_ID",
		Short: "Set the effort an orchestrator's Claude session launches at",
		Long: "Set which effort level this host-side orchestrator's Claude session launches at: one of " +
			levels + ". Pass \"" + orchestrator.OrchestratorLaunchSettingDefault + "\" to clear it back to the " +
			"default, " + ai.DefaultClaudeEffortLevel + ", the same level an environment's agent " +
			"launches at by default. " + orchestratorLaunchSettingStorage,
		Example: "  erun orchestrator set-effort my-orchestrator --effort high\n" +
			"  erun orchestrator set-effort my-orchestrator --effort " + orchestrator.OrchestratorLaunchSettingDefault,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			parsedEffort, err := orchestrator.ParseOrchestratorEffortFlag(effort)
			if err != nil {
				return err
			}
			return runOrchestratorSetEffortCommand(shared.CommandContext(cmd), store, orchestrator.SetOrchestratorEffortParams{
				OrchestratorID: args[0],
				Effort:         parsedEffort,
			})
		},
	}
	cmd.Flags().StringVar(&effort, "effort", "", fmt.Sprintf("Effort level (%s), or %q to clear it", levels, orchestrator.OrchestratorLaunchSettingDefault))
	if err := cmd.MarkFlagRequired("effort"); err != nil {
		panic(err)
	}
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runOrchestratorSetEffortCommand(ctx core.Context, store orchestrator.OrchestratorAliasStore, params orchestrator.SetOrchestratorEffortParams) error {
	if _, err := orchestrator.SetOrchestratorEffort(ctx, store, params); err != nil {
		return err
	}
	var err error
	if ctx.DryRun {
		_, err = fmt.Fprintln(ctx.Stdout, "Dry run: orchestrator effort update planned.")
		return err
	}
	_, err = fmt.Fprintf(ctx.Stdout, "Set effort %s for orchestrator %s\n", orchestratorLaunchSettingLabel(params.Effort), params.OrchestratorID)
	return err
}

// orchestratorLaunchSettingLabel names a cleared model or effort by the token
// that cleared it, so the confirmation reads back what the operator typed.
func orchestratorLaunchSettingLabel(value string) string {
	if value == "" {
		return orchestrator.OrchestratorLaunchSettingDefault
	}
	return value
}

func newOrchestratorSetAliasCmd(store orchestrator.OrchestratorAliasStore) *cobra.Command {
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
			"one. Pass \"" + orchestrator.OrchestratorAliasNone + "\" to declare no alias of its own again, " +
			"which is the default and leaves the orchestrator following this machine's own alias.",
		Example:      "  erun orchestrator set-alias my-orchestrator --alias erun+erunpaas.com@erun",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			parsedAlias, err := orchestrator.ParseOrchestratorAliasFlag(alias)
			if err != nil {
				return err
			}
			return runOrchestratorSetAliasCommand(shared.CommandContext(cmd), store, orchestrator.SetOrchestratorAliasParams{
				OrchestratorID: args[0],
				Alias:          parsedAlias,
			})
		},
	}
	cmd.Flags().StringVar(&alias, "alias", "", fmt.Sprintf("erun platform alias to act as, or %q to declare none", orchestrator.OrchestratorAliasNone))
	if err := cmd.MarkFlagRequired("alias"); err != nil {
		panic(err)
	}
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runOrchestratorSetAliasCommand(ctx core.Context, store orchestrator.OrchestratorAliasStore, params orchestrator.SetOrchestratorAliasParams) error {
	if _, err := orchestrator.SetOrchestratorAlias(ctx, store, params); err != nil {
		return err
	}
	var err error
	if ctx.DryRun {
		_, err = fmt.Fprintln(ctx.Stdout, "Dry run: orchestrator platform alias update planned.")
		return err
	}
	alias := orchestrator.OrchestratorAliasNone
	if params.Alias != "" {
		alias = params.Alias
	}
	_, err = fmt.Fprintf(ctx.Stdout, "Set platform alias %s for orchestrator %s\n", alias, params.OrchestratorID)
	return err
}

func newOrchestratorSetRoleCmd(store orchestrator.OrchestratorRoleStore) *cobra.Command {
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
			"takes any role except that one, having no pod for those to act on. Pass \"" + orchestrator.OrchestratorEnvRoleNone +
			"\" to declare it undeclared again; refused for a runtime-type environment, the same " +
			"way code or build is. The role is re-checked against the linked environment's type " +
			"every time, so this refuses the same pairings the desktop's link dialog would. The " +
			"environment must already be linked to the orchestrator -- see `erun list`.",
		Example:      "  erun orchestrator set-role my-orchestrator my-tenant prod --role build",
		Args:         cobra.ExactArgs(3),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			parsedRole, err := orchestrator.ParseOrchestratorEnvRoleFlag(role)
			if err != nil {
				return err
			}
			return runOrchestratorSetRoleCommand(shared.CommandContext(cmd), store, orchestrator.SetOrchestratorEnvRoleParams{
				OrchestratorID: args[0],
				Tenant:         args[1],
				Environment:    args[2],
				Role:           parsedRole,
			})
		},
	}
	cmd.Flags().StringVar(&role, "role", "", fmt.Sprintf("Role to set: %q, %q, %q, or %q for undeclared",
		config.OrchestratorEnvRoleCode, config.OrchestratorEnvRoleBuild, config.OrchestratorEnvRoleRuntime, orchestrator.OrchestratorEnvRoleNone))
	if err := cmd.MarkFlagRequired("role"); err != nil {
		panic(err)
	}
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runOrchestratorSetRoleCommand(ctx core.Context, store orchestrator.OrchestratorRoleStore, params orchestrator.SetOrchestratorEnvRoleParams) error {
	if _, err := orchestrator.SetOrchestratorEnvRole(ctx, store, params); err != nil {
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
