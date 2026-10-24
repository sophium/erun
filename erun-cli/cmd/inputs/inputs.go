package inputs

import (
	"fmt"

	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/cmd/shared/envscope"
	"github.com/sophium/erun/cmd/shared/textfmt"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/inputs"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/spf13/cobra"
)

// NewInputsCmd has no in-pod MCP counterpart: the edge cannot read host paths.
// erun mcp proxy serves the inputs_upload local tool instead.
func NewInputsCmd(resolveOpen envscope.OpenResolver) *cobra.Command {
	return clicmd.NewCommandGroup(
		"inputs",
		"Place a host file inside the runtime pod",
		newInputsUploadCmd(resolveOpen),
	)
}

func newInputsUploadCmd(resolveOpen envscope.OpenResolver) *cobra.Command {
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
			return runInputsUploadCommand(clicmd.CommandContext(cmd), resolveOpen, envscope.ScopedOpenParams(cmd.CommandPath(), tenant, environment), args[0], args[1], inputs.RunRemoteCommandWithStdin)
		},
	}
	clicmd.AddDryRunFlag(cmd)
	cmd.Flags().StringVar(&tenant, "tenant", "", "Target a specific tenant (default: the current scope)")
	cmd.Flags().StringVar(&environment, "environment", "", "Target a specific environment; requires --tenant")
	return cmd
}

func runInputsUploadCommand(ctx core.Context, resolveOpen envscope.OpenResolver, params model.OpenParams, localPath, remotePath string, run inputs.RuntimeInputUploadRunner) error {
	result, err := resolveOpen(params)
	if err != nil {
		return err
	}
	req := cloud.ShellLaunchParamsFromResult(result)
	out, err := inputs.UploadRuntimeInput(ctx, req, model.UploadRuntimeInputParams{LocalPath: localPath, RemotePath: remotePath}, run)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		return nil
	}
	if ctx.Output == model.OutputJSON {
		return ctx.WriteResult(out)
	}
	ctx.Info(fmt.Sprintf("Uploaded %s (%s, sha256 %s) to %s", localPath, textfmt.FormatOutputSize(out.Bytes), out.SHA256, out.RemotePath))
	return nil
}
