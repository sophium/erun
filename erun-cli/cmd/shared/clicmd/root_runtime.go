package clicmd

import (
	"github.com/spf13/cobra"
)

func AddCommands(parent *cobra.Command, commands ...*cobra.Command) {
	for _, command := range commands {
		if command == nil {
			continue
		}
		WrapCommandTreeWithElapsedTime(command)
		parent.AddCommand(command)
	}
}

func NewCommandGroup(use, short string, commands ...*cobra.Command) *cobra.Command {
	command := &cobra.Command{
		Use:           use,
		Short:         short,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	AddCommands(command, commands...)
	return command
}
