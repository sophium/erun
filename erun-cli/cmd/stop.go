package cmd

import (
	"fmt"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newStopCmd(resolveOpen func(common.OpenParams) (common.OpenResult, error), saveEnvConfig func(string, common.EnvConfig) error) *cobra.Command {
	target := common.OpenParams{}
	cmd := &cobra.Command{
		Use:   "stop [TENANT] [ENVIRONMENT]",
		Short: "Stop an environment's runtime and return its capacity to the node",
		Long: "Stop an environment's runtime and return its capacity to the node.\n\n" +
			"Scales the environment's runtime Deployment to zero, so both the runtime container's " +
			"resource limits and its unlimited dind sidecar's real usage go back to the cluster and " +
			"the environments you are actually using can be given more. Running work in the pod is " +
			"terminated. Persistent state is not touched — the home volume, the Docker/build caches, " +
			"and a builds-here environment's worktree all survive — so waking is a pod start, not a " +
			"rebuild. Desktop terminal sessions attached to the environment end with the pod; the " +
			"stop names them so you can see what it took down.\n\n" +
			"The stop is durable: it is recorded on the environment, so a later `erun deploy` " +
			"reconciles it rather than restarting the pod, and an automatic session reconnect " +
			"leaves it stopped. `erun open` is what wakes the environment again. Defaults to the " +
			"current scope; pass TENANT and ENVIRONMENT (or --tenant/--environment) to target another.",
		Example:      "  erun stop\n  erun stop team dev\n  erun stop team dev --dry-run\n  erun stop team dev --output json",
		Args:         cobra.MaximumNArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStopCommand(commandContext(cmd), args, target, resolveOpen, saveEnvConfig)
		},
	}
	addDryRunFlag(cmd)
	cmd.Flags().StringVar(&target.Tenant, "tenant", "", "Stop a specific tenant")
	cmd.Flags().StringVar(&target.Environment, "environment", "", "Stop a specific environment")
	return cmd
}

// runStopCommand deliberately does not run the cloud-context preflight every
// other cluster-touching command wires: that preflight starts a stopped cloud
// context, and starting a machine in order to stop a pod on it is the opposite
// of what the operator asked for.
func runStopCommand(ctx common.Context, args []string, overrides common.OpenParams, resolveOpen func(common.OpenParams) (common.OpenResult, error), saveEnvConfig func(string, common.EnvConfig) error) error {
	params, err := resolveOpenParams(args, overrides)
	if err != nil {
		return err
	}
	result, err := resolveOpen(params)
	if err != nil {
		return err
	}

	ctx, closeEnvTrace := common.ActivateEnvTrace(ctx, result.Tenant, result.Environment)
	defer closeEnvTrace()
	ctx.Trace(fmt.Sprintf("stop: tenant=%s environment=%s kubernetes-context=%s release=%s namespace=%s",
		result.Tenant, result.Environment, result.EnvConfig.KubernetesContext,
		common.RuntimeReleaseName(result.Tenant), common.KubernetesNamespaceName(result.Tenant, result.Environment)))
	if err := ctx.RequireKubernetesContext(result.EnvConfig.KubernetesContext); err != nil {
		return err
	}

	stopResult, err := common.RunStopEnvironment(ctx, common.StopEnvironmentParams{
		Result:        result,
		SaveEnvConfig: saveEnvConfig,
	})
	if err != nil {
		return err
	}
	if !ctx.DryRun && ctx.Output != common.OutputJSON {
		_, _ = fmt.Fprintf(ctx.Stdout, "%s\n", stopCommandSummary(stopResult))
	}
	return ctx.WriteResult(stopResult)
}

// stopCommandSummary names the ended sessions as well as the recovery: an
// operator whose desktop tabs go dark a second after the stop should read that
// as their own command doing what it said, not as the environment breaking.
func stopCommandSummary(result common.StopEnvironmentResult) string {
	if result.AlreadyStopped {
		return fmt.Sprintf("%s/%s was already stopped", result.Tenant, result.Environment)
	}
	sessions := ""
	if len(result.EndedSessions) > 0 {
		sessions = fmt.Sprintf(" and ended %d attached desktop session(s) (%s)", len(result.EndedSessions), strings.Join(result.EndedSessions, ", "))
	}
	return fmt.Sprintf("stopped %s/%s%s; run `erun open %s %s` to wake it", result.Tenant, result.Environment, sessions, result.Tenant, result.Environment)
}
