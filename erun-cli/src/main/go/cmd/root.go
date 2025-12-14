package cmd

import (
	"fmt"
	"os"

	"github.com/sophium/erun/internal/config"
	"github.com/spf13/cobra"
)

var (
	cfgFile string
	tenant  string
	cfg     *config.Config
	cfgPath string
	rootCmd = NewRootCmd()
)

// NewRootCmd builds a standalone instance of the root Cobra command.
func NewRootCmd() *cobra.Command {
	defaultTenant := os.Getenv("ERUN_TENANT")
	if defaultTenant == "" {
		defaultTenant = "default"
	}

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
	cmd.PersistentFlags().StringVar(&tenant, "tenant", defaultTenant, "tenant name used for configuration when --config is not set")
	cmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		return initConfig()
	}
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

func initConfig() error {
	loader := config.Loader{
		Tenant: tenant,
		Path:   cfgFile,
	}

	configured, path, err := loader.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	cfg = configured
	cfgPath = path
	return nil
}
