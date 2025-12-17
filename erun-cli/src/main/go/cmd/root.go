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

var log = internal.NewLogger(0)

func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "erun",
		Short: "Environment Runner",
		Long:  `erun helps to run and manage multiple tenants/environments.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
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
		if err := initConfig(); err != nil {
			cobra.CheckErr(err)
		}
	})
}

func initConfig() error {
	var err error
	var tenant string
	var path string
	var envName string

	log.Trace("Loading erun tool configuration")
	eRunConfig, err = internal.LoadERunConfig()

	log.Trace("If not initialized, try to detect current project directory")
	if errors.Is(err, internal.ErrNotInitialized) {
		tenant, path, err = internal.FindProjectRoot()

		log.Trace("Saving default config in case we managed to init it")
		eRunConfig.DefaultTenant = tenant
		if err := internal.SaveERunConfig(eRunConfig); err != nil {
			return err
		}
	} else {
		return err
	}

	log.Trace("Not initialized, not in project directory, instruct user to run erun in project directory")
	if errors.Is(err, internal.ErrNotInGitRepository) {
		log.Error("erun config is not initialized. Run erun in project directory.")
		return err
	}

	log.Trace("Some sort of fatal system error")
	if err != nil {
		log.Fatal(err)
		return err
	}

	tenantConfig, err = internal.LoadTenantConfig(tenant)

	log.Trace("If not initialized, add new tenant")
	if errors.Is(err, internal.ErrNotInitialized) {
		tenantConfig.ProjectRoot = path
		tenantConfig.DefaultEnvironment = "dev"
		if err := internal.SaveTenantConfig(tenantConfig); err != nil {
			return err
		}
	} else {
		return err
	}

	// TODO: environment must be either tenantConfig.DefaultEnvironment or one passed on to the tool
	envName = tenantConfig.DefaultEnvironment
	envConfig, err = internal.LoadEnvConfig(tenant, envName)

	log.Trace("If not initialized, add new environment")
	if errors.Is(err, internal.ErrNotInitialized) {
		envConfig.Name = envName
		if err := internal.SaveEnvConfig(tenant, envConfig); err != nil {
			return err
		}
	} else {
		return err
	}

	log.Trace("Configuration initialized OK")
	return nil
}
