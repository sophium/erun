package cmd

import (
	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

// newContributeCmd groups the `erun contribute …` subcommands that drive
// contribute-mode workflows.
func newContributeCmd(runGit common.GitCommandRunnerFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "contribute",
		Short: "Run contribute-mode workflows that target the ERun source repository",
	}
	cmd.AddCommand(newContributeCloneCmd(runGit))
	return cmd
}

func newContributeCloneCmd(runGit common.GitCommandRunnerFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "clone",
		Short:         "Clone the ERun repository into $HOME/git/erun for contribute mode if it is not already present",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			return common.RunContributeClone(ctx, "", runGit)
		},
	}
	addDryRunFlag(cmd)
	return cmd
}
