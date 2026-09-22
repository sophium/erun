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
			"usage cross a fixed threshold; the memory-peak and OOM-kill warnings also\n" +
			"consult the environment's retained history, because those cgroup counters\n" +
			"reset when the container restarts and an environment that has been\n" +
			"OOM-killed would otherwise read as memory-healthy. Every field reports its own unavailability\n" +
			"(cgroup v1, an unlimited limit, a file that could not be read) rather than\n" +
			"failing the call, since those are normal on some clusters, not errors.\n\n" +
			"Disk is reported for the whole mount (node, shared): every environment\n" +
			"scheduled on the same node sees the identical total/used/percent, so cleaning\n" +
			"up one environment may barely move it. The own-usage line beneath it (a `du`\n" +
			"of this environment's own directory) is the figure this environment can\n" +
			"actually act on.\n\n" +
			"On a build-capable environment (local-agent, remote-agent), every image build\n" +
			"actually runs in the erun-dind sidecar, a separate cgroup from the runtime\n" +
			"container above -- so this command also reads the sidecar's own CPU and\n" +
			"memory against its own limit and reports it alongside, with the same named\n" +
			"warnings if the sidecar itself nears its memory limit or records an OOM kill.\n" +
			"A busy build no longer reads as an idle environment. If the sidecar's own\n" +
			"cgroup could not be read, the output says so instead; `erun observe` reports\n" +
			"its resource limits either way.\n\n" +
			"The environment's standing sizing recommendation rides along with the reading\n" +
			"whenever the usage history behind it can be read: it is derived from history\n" +
			"the environment's own pod monitor retained, which lives with the environment.\n" +
			"Which surfaces show it follows from that, not from which command was run:\n" +
			"the `usage` and `resize` tools over the environment's MCP endpoint and\n" +
			"`erun list`'s `runtime-pod:` block all resolve the same recommendation from\n" +
			"the same evidence, so no two of them can disagree. A host that has never\n" +
			"monitored the environment has no history to resolve, and reports the reading\n" +
			"with no sizing verdict at all rather than a zero-value one.",
		Example: "  erun usage --tenant team --environment dev\n" +
			"  erun usage --tenant team --environment dev --interval 3 --output json",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUsageCommand(commandContext(cmd), resolveOpen, scopedOpenParams(cmd.CommandPath(), tenant, environment), intervalSeconds)
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
	report := common.ResolveRuntimeUsageReport(result.Tenant, result.EnvConfig, usage)
	if ctx.Output == common.OutputJSON {
		return ctx.WriteResult(report)
	}
	return writeUsageResult(ctx, report)
}
