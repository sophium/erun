package contribute

import (
	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/command/review"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/spf13/cobra"
)

// NewContributeCmd groups the `erun contribute …` subcommands.
func NewContributeCmd(runGit gitx.GitCommandRunnerFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "contribute",
		Short: "Run contribute-mode workflows that target the ERun source repository",
	}
	cmd.AddCommand(newContributeCloneCmd(runGit))
	return cmd
}

func newContributeCloneCmd(runGit gitx.GitCommandRunnerFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "clone",
		Short:         "Clone the ERun repository into $HOME/git/erun for contribute mode if it is not already present",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.CommandContext(cmd)
			return review.RunContributeClone(ctx, "", runGit)
		},
	}
	shared.AddDryRunFlag(cmd)
	return cmd
}
