package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	cfgFile string
	rootCmd = NewRootCmd()
)

// NewRootCmd builds a standalone instance of the root Cobra command.
func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "erun",
		Short: "Utility CLI placeholder built with Cobra",
		Long: `erun is a skeleton CLI built with Cobra.
It gives you a starting point for adding real commands and configuration.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Show help when no subcommand is provided so users can discover available commands.
			return cmd.Help()
		},
	}

	cmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path (optional)")
	cmd.AddCommand(NewVersionCmd())
	return cmd
}

// Execute runs the root command and surfaces any errors to the caller.
func Execute() error {
	if err := rootCmd.Execute(); err != nil {
		return fmt.Errorf("cli execution failed: %w", err)
	}
	return nil
}

func init() {
	cobra.OnInitialize(initConfig)
}

func initConfig() {
	if cfgFile == "" {
		return
	}

	if _, err := os.Stat(cfgFile); err != nil {
		// Surfacing errors via Cobra will already format them for the user.
		fmt.Fprintf(os.Stderr, "warning: unable to stat config file %s: %v\n", cfgFile, err)
		return
	}

	// TODO: implement configuration loading when needed.
}
