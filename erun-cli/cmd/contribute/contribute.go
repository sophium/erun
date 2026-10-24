package contribute

import (
	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/command/review"
	"github.com/spf13/cobra"
)

// NewContributeCmd groups the `erun contribute …` subcommands.
func NewContributeCmd(runGit seam.GitCommandRunnerFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "contribute",
		Short: "Run contribute-mode workflows that target the ERun source repository",
	}
	cmd.AddCommand(newContributeCloneCmd(runGit))
	return cmd
}

func newContributeCloneCmd(runGit seam.GitCommandRunnerFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "clone",
		Short:         "Clone the ERun repository into $HOME/git/erun for contribute mode if it is not already present",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := clicmd.CommandContext(cmd)
			return review.RunContributeClone(ctx, "", runGit)
		},
	}
	clicmd.AddDryRunFlag(cmd)
	return cmd
}
