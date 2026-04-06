package cmd

import (
	"errors"
	"time"

	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/internal"
	"github.com/sophium/erun/internal/bootstrap"
	"github.com/sophium/erun/internal/opener"
	"github.com/spf13/cobra"
)

type (
	PromptRunner                func(promptui.Prompt) (string, error)
	SelectRunner                func(promptui.Select) (int, string, error)
	KubernetesContextsLister    func() ([]string, error)
	KubernetesNamespaceEnsurer  func(string, string) error
	BuildContextResolver        func() (DockerBuildContext, error)
	DeployContextResolver       func() (KubernetesDeployContext, error)
	KubernetesDeploymentChecker func(KubernetesDeploymentCheckRequest) (bool, error)
	DockerImageBuilder          func(DockerBuildRequest) error
	DockerImagePusher           func(DockerPushRequest) error
	DockerRegistryLogin         func(DockerLoginRequest) error
	HelmChartDeployer           func(HelmDeployRequest) error
	NowFunc                     func() time.Time
)

type Dependencies struct {
	Store                          bootstrap.Store
	FindProjectRoot                bootstrap.ProjectFinder
	PromptRunner                   PromptRunner
	SelectRunner                   SelectRunner
	ListKubernetesContexts         KubernetesContextsLister
	EnsureKubernetesNamespace      KubernetesNamespaceEnsurer
	ResolveDockerBuildContext      BuildContextResolver
	ResolveKubernetesDeployContext DeployContextResolver
	CheckKubernetesDeployment      KubernetesDeploymentChecker
	BuildDockerImage               DockerImageBuilder
	PushDockerImage                DockerImagePusher
	LoginToDockerRegistry          DockerRegistryLogin
	DeployHelmChart                HelmChartDeployer
	LaunchShell                    opener.ShellLauncher
	Now                            NowFunc
}

func DefaultDependencies() Dependencies {
	return Dependencies{
		Store:                          bootstrap.ConfigStore{},
		FindProjectRoot:                internal.FindProjectRoot,
		PromptRunner:                   defaultPromptRunner,
		ListKubernetesContexts:         defaultKubernetesContextsLister,
		EnsureKubernetesNamespace:      defaultKubernetesNamespaceEnsurer,
		ResolveDockerBuildContext:      defaultDockerBuildContextResolver,
		ResolveKubernetesDeployContext: defaultKubernetesDeployContextResolver,
		CheckKubernetesDeployment:      defaultKubernetesDeploymentChecker,
		BuildDockerImage:               defaultDockerImageBuilder,
		PushDockerImage:                defaultDockerImagePusher,
		LoginToDockerRegistry:          defaultDockerRegistryLogin,
		DeployHelmChart:                defaultHelmChartDeployer,
		LaunchShell:                    opener.DefaultShellLauncher,
		Now:                            time.Now,
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
		Long:             "erun helps to run and manage multiple tenants/environments.\n\nVerbosity levels:\n  -v    print resolved command plans before execution\n  -vv   add decision notes\n  -vvv  include internal trace logs when available\n\nDry-run:\n  --dry-run prints resolved command plans without executing them\n  --dry-run -v adds decision notes\n  --dry-run -vv adds internal trace logs when available",
		Example:          "  erun deploy --dry-run\n  erun -v deploy --dry-run\n  erun -vv init -y\n  eval \"$(erun open --no-shell)\"",
		Args:             cobra.MaximumNArgs(2),
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch len(args) {
			case 0:
				err := runOpenCommand(cmd, deps, opener.Request{
					UseDefaultTenant:      true,
					UseDefaultEnvironment: true,
				}, openOptions{})
				if shouldInitRootCommand(err) {
					initReq, initErr := initRequestForRootCommand(deps, args)
					if initErr != nil {
						return initErr
					}
					return runInitCommand(cmd, deps, &verbosity, initReq)
				}
				return err
			case 1:
				err := runOpenCommand(cmd, deps, opener.Request{
					Environment:      args[0],
					UseDefaultTenant: true,
				}, openOptions{})
				if shouldInitRootCommand(err) {
					initReq, initErr := initRequestForRootCommand(deps, args)
					if initErr != nil {
						return initErr
					}
					return runInitCommand(cmd, deps, &verbosity, initReq)
				}
				return err
			case 2:
				err := runOpenCommand(cmd, deps, opener.Request{
					Tenant:      args[0],
					Environment: args[1],
				}, openOptions{})
				if shouldInitRootCommand(err) {
					initReq, initErr := initRequestForRootCommand(deps, args)
					if initErr != nil {
						return initErr
					}
					return runInitCommand(cmd, deps, &verbosity, initReq)
				}
				return err
			default:
				return cobra.MaximumNArgs(2)(cmd, args)
			}
		},
	}

	addDryRunFlag(cmd)
	cmd.PersistentFlags().CountVarP(&verbosity, "verbose", "v", verboseFlagUsage)
	cmd.AddCommand(NewInitCmd(deps, &verbosity))
	cmd.AddCommand(NewOpenCmd(deps, &verbosity))
	cmd.AddCommand(NewDevopsCmd(deps))
	if buildContexts, _, err := resolveCurrentDockerBuildContexts(deps); err == nil && len(buildContexts) > 0 {
		cmd.AddCommand(NewBuildCmd(deps))
	}
	if buildContext, err := deps.ResolveDockerBuildContext(); err == nil && buildContext.DockerfilePath != "" {
		cmd.AddCommand(NewPushCmd(deps))
	}
	if deployContext, err := deps.ResolveKubernetesDeployContext(); err == nil && deployContext.ChartPath != "" {
		cmd.AddCommand(NewDeployCmd(deps))
	}
	cmd.AddCommand(NewMCPCmd(deps, &verbosity))
	cmd.AddCommand(NewVersionCmd(deps, &verbosity))
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
	if deps.PromptRunner == nil {
		deps.PromptRunner = defaultPromptRunner
	}
	if deps.SelectRunner == nil {
		deps.SelectRunner = defaultSelectRunner
	}
	if deps.ListKubernetesContexts == nil {
		deps.ListKubernetesContexts = defaultKubernetesContextsLister
	}
	if deps.ResolveDockerBuildContext == nil {
		deps.ResolveDockerBuildContext = defaultDockerBuildContextResolver
	}
	if deps.ResolveKubernetesDeployContext == nil {
		deps.ResolveKubernetesDeployContext = defaultKubernetesDeployContextResolver
	}
	if deps.BuildDockerImage == nil {
		deps.BuildDockerImage = defaultDockerImageBuilder
	}
	if deps.PushDockerImage == nil {
		deps.PushDockerImage = defaultDockerImagePusher
	}
	if deps.LoginToDockerRegistry == nil {
		deps.LoginToDockerRegistry = defaultDockerRegistryLogin
	}
	if deps.DeployHelmChart == nil {
		deps.DeployHelmChart = defaultHelmChartDeployer
	}
	if deps.LaunchShell == nil {
		deps.LaunchShell = opener.DefaultShellLauncher
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return deps
}

func shouldInitRootCommand(err error) bool {
	return openerIsDefaultError(err) || shouldInitOpenCommand(err) || internal.IsReported(err)
}

func initRequestForRootCommand(deps Dependencies, args []string) (bootstrap.InitRequest, error) {
	if len(args) == 2 {
		return bootstrap.InitRequest{
			Tenant:      args[0],
			Environment: args[1],
		}, nil
	}

	envName := ""
	if len(args) == 1 {
		envName = args[0]
	}

	tenant, err := loadDefaultTenant(deps.Store)
	if err != nil {
		if errors.Is(err, opener.ErrDefaultTenantNotConfigured) || errors.Is(err, internal.ErrNotInitialized) {
			return bootstrap.InitRequest{
				Environment:   envName,
				ResolveTenant: true,
			}, nil
		}
		return bootstrap.InitRequest{}, err
	}

	if envName != "" {
		return bootstrap.InitRequest{
			Tenant:      tenant,
			Environment: envName,
		}, nil
	}

	defaultEnvironment, err := loadDefaultEnvironment(deps.Store, tenant)
	if err != nil {
		if errors.Is(err, opener.ErrDefaultEnvironmentNotConfigured) || errors.Is(err, internal.ErrNotInitialized) {
			return bootstrap.InitRequest{Tenant: tenant}, nil
		}
		return bootstrap.InitRequest{}, err
	}

	return bootstrap.InitRequest{
		Tenant:      tenant,
		Environment: defaultEnvironment,
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
