package cmd

import (
	"errors"

	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/internal"
	"github.com/sophium/erun/internal/bootstrap"
	"github.com/sophium/erun/internal/opener"
	"github.com/spf13/cobra"
)

type (
	PromptRunner func(promptui.Prompt) (string, error)
	SelectRunner func(promptui.Select) (int, string, error)
)

type Dependencies struct {
	Store             bootstrap.Store
	FindProjectRoot   bootstrap.ProjectFinder
	FindCurrentBranch bootstrap.CurrentBranchFinder
	PromptRunner      PromptRunner
	SelectRunner      SelectRunner
	LaunchShell       opener.ShellLauncher
}

func DefaultDependencies() Dependencies {
	return Dependencies{
		Store:             bootstrap.ConfigStore{},
		FindProjectRoot:   internal.FindProjectRoot,
		FindCurrentBranch: internal.FindCurrentBranch,
		PromptRunner:      defaultPromptRunner,
		LaunchShell:       opener.DefaultShellLauncher,
	}
}

var defaultPromptRunner = func(prompt promptui.Prompt) (string, error) {
	return prompt.Run()
}

var defaultSelectRunner = func(prompt promptui.Select) (int, string, error) {
	return prompt.Run()
}

func NewRootCmd(deps Dependencies) *cobra.Command {
	deps = withDependencyDefaults(deps)
	var verbosity int

	cmd := &cobra.Command{
		Use:              "erun",
		Short:            "Environment Runner",
		Long:             `erun helps to run and manage multiple tenants/environments.`,
		Args:             cobra.MaximumNArgs(2),
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch len(args) {
			case 0:
				result, err := resolveOpenCommand(deps, opener.Request{
					UseDefaultTenant:      true,
					UseDefaultEnvironment: true,
				})
				if err != nil {
					if shouldInitRootCommand(err) {
						initReq, initErr := initRequestForRootCommand(deps, args)
						if initErr != nil {
							return initErr
						}
						return runInitCommand(cmd, deps, &verbosity, initReq)
					}
					return err
				}
				return launchOpenResult(deps, result)
			case 1:
				result, err := resolveOpenCommand(deps, opener.Request{
					Environment:      args[0],
					UseDefaultTenant: true,
				})
				if err != nil {
					if shouldInitRootCommand(err) {
						initReq, initErr := initRequestForRootCommand(deps, args)
						if initErr != nil {
							return initErr
						}
						return runInitCommand(cmd, deps, &verbosity, initReq)
					}
					return err
				}
				return launchOpenResult(deps, result)
			case 2:
				result, err := resolveOpenCommand(deps, opener.Request{
					Tenant:      args[0],
					Environment: args[1],
				})
				if err != nil {
					return err
				}
				return launchOpenResult(deps, result)
			default:
				return cobra.MaximumNArgs(2)(cmd, args)
			}
		},
	}

	cmd.PersistentFlags().CountVarP(&verbosity, "verbose", "v", "Increase logging verbosity. Repeat for more detail.")
	cmd.AddCommand(NewInitCmd(deps, &verbosity))
	cmd.AddCommand(NewOpenCmd(deps))
	cmd.AddCommand(NewMCPCmd(deps))
	cmd.AddCommand(NewVersionCmd())
	return cmd
}

func Execute() error {
	return NewRootCmd(DefaultDependencies()).Execute()
}

func withDependencyDefaults(deps Dependencies) Dependencies {
	if deps.Store == nil {
		deps.Store = bootstrap.ConfigStore{}
	}
	if deps.FindProjectRoot == nil {
		deps.FindProjectRoot = internal.FindProjectRoot
	}
	if deps.FindCurrentBranch == nil {
		deps.FindCurrentBranch = internal.FindCurrentBranch
	}
	if deps.PromptRunner == nil {
		deps.PromptRunner = defaultPromptRunner
	}
	if deps.SelectRunner == nil {
		deps.SelectRunner = defaultSelectRunner
	}
	if deps.LaunchShell == nil {
		deps.LaunchShell = opener.DefaultShellLauncher
	}
	return deps
}

func shouldInitRootCommand(err error) bool {
	return openerIsDefaultError(err) || internal.IsReported(err)
}

func initRequestForRootCommand(deps Dependencies, args []string) (bootstrap.InitRequest, error) {
	envName := ""
	if len(args) == 1 {
		envName = args[0]
	}

	tenant, err := loadDefaultTenant(deps.Store)
	if err != nil {
		if errors.Is(err, opener.ErrDefaultTenantNotConfigured) || errors.Is(err, internal.ErrNotInitialized) {
			return bootstrap.InitRequest{
				Environment:             envName,
				DetectEnvironmentBranch: true,
				ResolveTenant:           true,
			}, nil
		}
		return bootstrap.InitRequest{}, err
	}

	if envName != "" {
		return bootstrap.InitRequest{
			Tenant:                  tenant,
			Environment:             envName,
			DetectEnvironmentBranch: true,
		}, nil
	}

	defaultEnvironment, err := loadDefaultEnvironment(deps.Store, tenant)
	if err != nil {
		if errors.Is(err, opener.ErrDefaultEnvironmentNotConfigured) || errors.Is(err, internal.ErrNotInitialized) {
			return bootstrap.InitRequest{Tenant: tenant, DetectEnvironmentBranch: true}, nil
		}
		return bootstrap.InitRequest{}, err
	}

	return bootstrap.InitRequest{
		Tenant:                  tenant,
		Environment:             defaultEnvironment,
		DetectEnvironmentBranch: true,
	}, nil
}

func loadDefaultTenant(store bootstrap.Store) (string, error) {
	toolConfig, _, err := store.LoadERunConfig()
	if errors.Is(err, internal.ErrNotInitialized) {
		return "", opener.ErrDefaultTenantNotConfigured
	}
	if err != nil {
		return "", err
	}
	if toolConfig.DefaultTenant == "" {
		return "", opener.ErrDefaultTenantNotConfigured
	}
	return toolConfig.DefaultTenant, nil
}

func loadDefaultEnvironment(store bootstrap.Store, tenant string) (string, error) {
	tenantConfig, _, err := store.LoadTenantConfig(tenant)
	if err != nil {
		return "", err
	}
	if tenantConfig.DefaultEnvironment == "" {
		return "", opener.ErrDefaultEnvironmentNotConfigured
	}
	return tenantConfig.DefaultEnvironment, nil
}
