package cmd

import (
	"errors"
	"fmt"

	"github.com/sophium/erun/internal"
	"github.com/spf13/cobra"
)

var rootCmd = NewRootCmd()

var (
	eRunConfig   internal.ERunConfig
	tenantConfig internal.TenantConfig
	envConfig    internal.EnvConfig
)

var (
	log       = internal.NewLogger(0)
	verbosity int
)

func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "erun",
		Short: "Environment Runner",
		Long:  `erun helps to run and manage multiple tenants/environments.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.PersistentFlags().CountVarP(&verbosity, "verbose", "v", "Increase logging verbosity. Repeat for more detail.")
	cmd.AddCommand(NewVersionCmd())
	return cmd
}

func Execute() error {
	if err := rootCmd.Execute(); err != nil {
		return fmt.Errorf("cli execution failed: %w", err)
	}
	return nil
}

func init() {
	cobra.OnInitialize(func() {
		configureLogging()
		if err := initConfig(); err != nil {
			cobra.CheckErr(err)
		}
	})
}

func configureLogging() {
	log = internal.NewLogger(verbosity)
}

func initConfig() error {
	var err error
	var tenant string
	var path string
	var envName string
	var configPath string

	eRunConfig, configPath, err = internal.LoadERunConfig()
	log.Trace("Loading erun tool configuration, configPath=" + configPath)

	if errors.Is(err, internal.ErrNotInitialized) {
		log.Trace("Trying to detect current project directory")
		tenant, path, err = internal.FindProjectRoot()

		if errors.Is(err, internal.ErrNotInGitRepository) {
			log.Error("erun config is not initialized. Run erun in project directory.")
			return err
		}
		if err != nil {
			log.Trace("Some sort of fatal system error")
			log.Fatal(err)
			return err
		}

		log.Trace("Saving default config")
		eRunConfig.DefaultTenant = tenant
		if err := internal.SaveERunConfig(eRunConfig); err != nil {
			return err
		}
	}

	if err != nil {
		log.Trace("Some sort of fatal system error")
		log.Fatal(err)
		return err
	}
	log.Trace("Loaded erun tool configuration")

	log.Trace("Loading tenant configuration")
	tenantConfig, _, err = internal.LoadTenantConfig(tenant)

	if errors.Is(err, internal.ErrNotInitialized) {
		log.Trace("Adding new tenant")
		tenantConfig.ProjectRoot = path
		tenantConfig.DefaultEnvironment = "dev"
		tenantConfig.Name = tenant
		if err := internal.SaveTenantConfig(tenantConfig); err != nil {
			return err
		}
		err = nil
	}

	if err != nil {
		log.Trace("Some sort of fatal system error")
		log.Fatal(err)
		return err
	}

	log.Trace("Loaded tenant configuration")
	log.Trace("Loading environment configuration")

	// TODO: environment must be either tenantConfig.DefaultEnvironment or one passed on to the tool
	envName = tenantConfig.DefaultEnvironment
	envConfig, _, err = internal.LoadEnvConfig(tenant, envName)

	if errors.Is(err, internal.ErrNotInitialized) {
		log.Trace("Adding new environment")
		envConfig.Name = envName
		if err := internal.SaveEnvConfig(tenant, envConfig); err != nil {
			return err
		}
		err = nil
	}

	if err != nil {
		log.Trace("Some sort of fatal system error")
		log.Fatal(err)
		return err
	}

	log.Trace("Configuration initialized OK")
	return nil
}
