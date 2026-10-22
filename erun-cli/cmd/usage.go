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
			"Those figures are limits, and a limit is a ceiling: it reserves nothing at\n" +
			"scheduling time, so `394.4MiB / 27.0GiB limit` is a reading against a\n" +
			"ceiling, not a claim that this environment holds 27.0GiB. What the\n" +
			"scheduler actually admits the pod on is its resources.requests, so this\n" +
			"command reports those too, read from the live pod spec -- no cgroup file\n" +
			"records a request. Each container's own request is listed under the pod's\n" +
			"effective total, which is the rule Kubernetes itself admits on: the larger\n" +
			"of the init containers' peak and the sum of the containers'. A pod spec\n" +
			"that cannot be read reports that, rather than a reservation of nothing.\n\n" +
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
			"The standing sizing recommendation printed beneath the reading is derived\n" +
			"from this reading together with the environment's retained usage history --\n" +
			"the same evidence, and the same verdict, the `usage`/`resize` tools over the\n" +
			"environment's MCP endpoint and the desktop Runtime tab report. That history\n" +
			"is retained by the environment's own pod, so a reading taken from a host has\n" +
			"the live counters to reason from and no observed window: the advice the\n" +
			"counters prove (a raise, after a peak at the limit or a recorded OOM kill) is\n" +
			"reported there too, while the shrink direction, which needs a day of quiet\n" +
			"evidence, reads as insufficient-evidence. `erun list` prints the window-only\n" +
			"view under `runtime-pod:`, and only when run inside the environment, where\n" +
			"that history lives.",
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
	usage, err := common.RunRuntimeUsage(ctx, nil, nil, req, common.RuntimeUsageParams{
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
