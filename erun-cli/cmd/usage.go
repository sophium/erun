package cmd

import (
	"time"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newUsageCmd(resolveOpen OpenResolver) *cobra.Command {
	var tenant, environment string
	var intervalSeconds float64
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Report an environment's live CPU, memory, and disk usage",
		Long: "Read CPU quota utilisation, memory against the runtime container's own cgroup\n" +
			"limit, and disk usage for the workspace mount, straight from the runtime\n" +
			"container's cgroup v2 accounting and a statfs of the workspace mount.\n\n" +
			"No metrics-server is required: this works on clusters where `kubectl top`\n" +
			"reports \"Metrics API not available\", which is every local (orbstack/k3s-style)\n" +
			"cluster. Memory is reported against the container's own limit (current usage,\n" +
			"the peak high-water mark, and a real OOM-kill count from the cgroup, replacing\n" +
			"a post-mortem guess); CPU utilisation is measured against its quota over a\n" +
			"sample interval. A named warning fires when memory, memory's peak, or disk\n" +
			"usage cross a fixed threshold. Every field reports its own unavailability\n" +
			"(cgroup v1, an unlimited limit, a file that could not be read) rather than\n" +
			"failing the call, since those are normal on some clusters, not errors.\n\n" +
			"Disk is reported for the whole mount (node, shared): every environment\n" +
			"scheduled on the same node sees the identical total/used/percent, so cleaning\n" +
			"up one environment may barely move it. The own-usage line beneath it (a `du`\n" +
			"of this environment's own directory) is the figure this environment can\n" +
			"actually act on.\n\n" +
			"On a build-capable environment (local-agent, remote-agent), CPU and memory\n" +
			"are scoped to this container alone: every image build actually runs in the\n" +
			"erun-dind sidecar, a separate cgroup this reading cannot see, so a busy build\n" +
			"can show as idle here. The output states this exclusion explicitly on those\n" +
			"environments; `erun observe` reports the sidecar's own resource limits.",
		Example: "  erun usage --tenant team --environment dev\n" +
			"  erun usage --tenant team --environment dev --interval 3 --output json",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUsageCommand(commandContext(cmd), resolveOpen, scopedOpenParams(tenant, environment), intervalSeconds)
		},
	}
	addDryRunFlag(cmd)
	cmd.Flags().StringVar(&tenant, "tenant", "", "Target a specific tenant (default: the current scope)")
	cmd.Flags().StringVar(&environment, "environment", "", "Target a specific environment; requires --tenant")
	cmd.Flags().Float64Var(&intervalSeconds, "interval", 1, "CPU sample window in seconds, clamped to 0.1-30: usage is read, the window elapses, then it is read again so utilisation is a rate rather than a cumulative counter")
	return cmd
}

func runUsageCommand(ctx common.Context, resolveOpen OpenResolver, params common.OpenParams, intervalSeconds float64) error {
	result, err := resolveOpen(params)
	if err != nil {
		return err
	}
	req := common.ShellLaunchParamsFromResult(result)
	usage, err := common.RunRuntimeUsage(ctx, nil, req, common.RuntimeUsageParams{
		Interval: time.Duration(intervalSeconds * float64(time.Second)),
	})
	if err != nil {
		return err
	}
	if ctx.DryRun {
		return nil
	}
	if ctx.Output == common.OutputJSON {
		return ctx.WriteResult(usage)
	}
	return writeUsageResult(ctx, usage)
}
