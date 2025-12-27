package cmd

import "github.com/spf13/cobra"

func NewInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize configuration for the current project",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Root().Help()
		},
	}
}
