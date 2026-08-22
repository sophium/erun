package cmd

import (
	"fmt"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

// newInputsCmd has no in-pod MCP counterpart: the edge in erun-mcp runs
// inside the pod and cannot read a path on the operator's host. An
// MCP-connected orchestrator reaches the same transfer through the
// inputs_upload local tool erun mcp proxy serves (see mcp_proxy.go),
// mirroring how workspace_sync is host-served rather than edge-served.
func newInputsCmd(resolveOpen OpenResolver) *cobra.Command {
	return newCommandGroup(
		"inputs",
		"Place a host file inside the runtime pod",
		newInputsUploadCmd(resolveOpen),
	)
}

func newInputsUploadCmd(resolveOpen OpenResolver) *cobra.Command {
	var tenant, environment string
	cmd := &cobra.Command{
		Use:   "upload <local-path> <remote-path>",
		Short: "Stream a local file into the runtime pod at an explicit destination",
		Long: "Place a file from this machine inside the environment's runtime pod, byte-identical, " +
			"without shell-encoding it and without the bytes passing through an agent's context.\n\n" +
			"<remote-path> is the full absolute destination inside the pod, including the file name — " +
			"there is no default location, so a transfer can never silently land somewhere a background " +
			"process (such as the workspace-sync mirror) reconciles away. The destination directory is " +
			"created if missing; the command refuses clearly, naming why, if it is not writable or the " +
			"MCP-adjacent channel to the pod is down. After the transfer, the local and remote sha256 are " +
			"compared and a mismatch fails the command.",
		Example:       "  erun inputs upload ./evidence.xlsx /home/erun/.erun/outputs/evidence.xlsx",
		Args:          cobra.ExactArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInputsUploadCommand(commandContext(cmd), resolveOpen, scopedOpenParams(tenant, environment), args[0], args[1], common.RunRemoteCommandWithStdin)
		},
	}
	addDryRunFlag(cmd)
	cmd.Flags().StringVar(&tenant, "tenant", "", "Target a specific tenant (default: the current scope)")
	cmd.Flags().StringVar(&environment, "environment", "", "Target a specific environment; requires --tenant")
	return cmd
}

func runInputsUploadCommand(ctx common.Context, resolveOpen OpenResolver, params common.OpenParams, localPath, remotePath string, run common.RuntimeInputUploadRunner) error {
	result, err := resolveOpen(params)
	if err != nil {
		return err
	}
	req := common.ShellLaunchParamsFromResult(result)
	out, err := common.UploadRuntimeInput(ctx, req, common.UploadRuntimeInputParams{LocalPath: localPath, RemotePath: remotePath}, run)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		return nil
	}
	if ctx.Output == common.OutputJSON {
		return ctx.WriteResult(out)
	}
	ctx.Info(fmt.Sprintf("Uploaded %s (%s, sha256 %s) to %s", localPath, formatOutputSize(out.Bytes), out.SHA256, out.RemotePath))
	return nil
}
