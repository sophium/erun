package dind

import (
	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/podruntime"
	"github.com/spf13/cobra"
)

// NewDindEntrypointCmd is the erun-dind sidecar's entrypoint; an init container
// copies this binary in because docker:dind carries no erun.
func NewDindEntrypointCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dind-entrypoint [-- DOCKERD_ARGS...]",
		Short: "Start the erun-dind sidecar's dockerd at the pod's MTU with builds CPU-capped (Linux sidecar only)",
		Long: "Prepares the erun-dind sidecar and then replaces itself with the stock\n" +
			"dockerd-entrypoint.sh, passing DOCKERD_ARGS through.\n\n" +
			"It reads the MTU of the interface carrying the pod's default route and leads the\n" +
			"daemon's args with --mtu, so builds behind an encapsulating CNI do not stall on\n" +
			"full-size packets. It mirrors the sidecar's own cpu.max into the per-pod cgroup\n" +
			"docker/erun-build-cpu-cap-<pod> that in-pod builds nest under with --cgroup-parent.\n" +
			"Neither step is fatal: when one cannot apply it says so and dockerd starts on its\n" +
			"defaults. Runs only on Linux, inside the sidecar; --dry-run reports both steps and\n" +
			"the dockerd command without writing a cgroup or starting the daemon.",
		Example: "  erun dind-entrypoint -- --insecure-registry registry.cluster.local:5000\n" +
			"  erun dind-entrypoint --dry-run",
		Args:          cobra.ArbitraryArgs,
		Hidden:        true,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return podruntime.RunDindEntrypoint(clicmd.CommandContext(cmd), model.DindEntrypointParams{DockerdArgs: args})
		},
	}
	clicmd.AddDryRunFlag(cmd)
	return cmd
}
