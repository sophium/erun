package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/internal"
	"github.com/spf13/cobra"
)

var rootCmd = NewRootCmd()

const defaultEnvironment = "dev"

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
	envName := defaultEnvironment
	var configPath string

	eRunConfig, configPath, err = internal.LoadERunConfig()
	log.Trace("Loading erun tool configuration, configPath=" + configPath)
	if tenant == "" {
		tenant = eRunConfig.DefaultTenant
	}

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

		confirm, promptErr := confirmTenantInitialization(tenant, path, envName)
		if promptErr != nil {
			return promptErr
		}
		if !confirm {
			return fmt.Errorf("tenant initialization cancelled by user")
		}

		log.Trace("Saving default config")
		eRunConfig.DefaultTenant = tenant
		if err := internal.SaveERunConfig(eRunConfig); err != nil {
			return err
		}
		tenant = eRunConfig.DefaultTenant
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
		tenantConfig.DefaultEnvironment = envName
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
		confirmEnv, promptErr := confirmEnvironmentInitialization(tenant, envName)
		if promptErr != nil {
			return promptErr
		}
		if !confirmEnv {
			return fmt.Errorf("environment initialization cancelled by user")
		}
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

func confirmTenantInitialization(tenant, path, envName string) (bool, error) {
	label := fmt.Sprintf(
		"Initialize tenant %q (path: %s) as the default tenant?",
		tenant,
		path,
	)
	return confirmPrompt(label)
}

func confirmEnvironmentInitialization(tenant, envName string) (bool, error) {
	label := fmt.Sprintf(
		"Initialize default environment %q for tenant %q?",
		envName,
		tenant,
	)
	return confirmPrompt(label)
}

func confirmPrompt(label string) (bool, error) {
	prompt := promptui.Prompt{
		Label:     label,
		IsConfirm: true,
		Default:   "y",
	}

	result, err := prompt.Run()
	if err != nil {
		if errors.Is(err, promptui.ErrInterrupt) {
			return false, fmt.Errorf("initialization interrupted")
		}
		return false, err
	}

	if result == "" {
		return true, nil
	}

	return strings.EqualFold(result, "y"), nil
}
