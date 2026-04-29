package eruncommon

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/xdg"
	"gopkg.in/yaml.v3"
)

const (
	DefaultEnvironment       = "local"
	DefaultContainerRegistry = "erunpaas"
	InitializeCurrentProject = "Initialize current project"
)

var (
	ErrTenantInitializationCancelled      = errors.New("tenant initialization cancelled by user")
	ErrEnvironmentInitializationCancelled = errors.New("environment initialization cancelled by user")
	ErrKubernetesContextCancelled         = errors.New("kubernetes context association cancelled by user")
	ErrContainerRegistryCancelled         = errors.New("container registry configuration cancelled by user")
	ErrTenantSelectionCancelled           = errors.New("tenant selection cancelled by user")
)

type BootstrapStore interface {
	LoadERunConfig() (ERunConfig, string, error)
	SaveERunConfig(ERunConfig) error
	ListTenantConfigs() ([]TenantConfig, error)
	LoadTenantConfig(string) (TenantConfig, string, error)
	SaveTenantConfig(TenantConfig) error
	LoadEnvConfig(string, string) (EnvConfig, string, error)
	SaveEnvConfig(string, EnvConfig) error
}

type (
	ProjectFinderFunc       func() (string, string, error)
	WorkDirFunc             func() (string, error)
	SelectTenantFunc        func([]TenantConfig) (TenantSelectionResult, error)
	PromptValueFunc         func(string) (string, error)
	NamespaceEnsurerFunc    func(string, string) error
	ProjectConfigLoaderFunc func(string) (ProjectConfig, string, error)
	ProjectConfigSaverFunc  func(string, ProjectConfig) error
	RemoteRuntimeWaitFunc   func(ShellLaunchParams) error
	RemoteCommandRunnerFunc func(ShellLaunchParams, string) (RemoteCommandResult, error)
	SleepFunc               func(time.Duration)
)

type (
	ConfirmFunc           func(label string) (bool, error)
	TenantSelectionResult struct {
		Tenant     string
		Initialize bool
	}
	RemoteCommandResult struct {
		Stdout string
		Stderr string
	}
)

type BootstrapInitParams struct {
	Tenant                   string
	SelectedTenant           string
	InitializeCurrentProject bool
	ProjectRoot              string
	Environment              string
	RuntimeVersion           string
	RuntimeImage             string
	NoGit                    bool
	KubernetesContext        string
	ContainerRegistry        string
	Remote                   bool
	RemoteRepositoryURL      string
	CodeCommitSSHKeyID       string
	Bootstrap                bool
	ConfirmTenant            *bool
	ConfirmEnvironment       *bool
	ConfirmRemoteHostConfig  *bool
	ConfirmRemoteKeyImport   *bool
	AutoApprove              bool
	ResolveTenant            bool
}

type BootstrapInitInteractionType string

const (
	BootstrapInitInteractionSelectTenant       BootstrapInitInteractionType = "select_tenant"
	BootstrapInitInteractionConfirmTenant      BootstrapInitInteractionType = "confirm_tenant"
	BootstrapInitInteractionConfirmEnvironment BootstrapInitInteractionType = "confirm_environment"
	BootstrapInitInteractionKubernetesContext  BootstrapInitInteractionType = "input_kubernetes_context"
	BootstrapInitInteractionContainerRegistry  BootstrapInitInteractionType = "input_container_registry"
	BootstrapInitInteractionRemoteRepository   BootstrapInitInteractionType = "input_remote_repository"
	BootstrapInitInteractionCodeCommitSSHKeyID BootstrapInitInteractionType = "input_codecommit_ssh_key_id"
	BootstrapInitInteractionConfirmRemoteHost  BootstrapInitInteractionType = "confirm_remote_host_config"
	BootstrapInitInteractionConfirmRemoteKey   BootstrapInitInteractionType = "confirm_remote_key"
)

type BootstrapInitInteraction struct {
	Type         BootstrapInitInteractionType `json:"type"`
	Label        string                       `json:"label"`
	Options      []string                     `json:"options,omitempty"`
	DefaultValue string                       `json:"defaultValue,omitempty"`
	Details      string                       `json:"details,omitempty"`
}

type BootstrapInitInteractionError struct {
	Interaction BootstrapInitInteraction
}

func (e BootstrapInitInteractionError) Error() string {
	return "bootstrap init interaction required: " + string(e.Interaction.Type)
}

func AsBootstrapInitInteraction(err error) (BootstrapInitInteraction, bool) {
	var interactionErr BootstrapInitInteractionError
	if !errors.As(err, &interactionErr) {
		return BootstrapInitInteraction{}, false
	}
	return interactionErr.Interaction, true
}

type BootstrapInitResult struct {
	ERunConfig          ERunConfig
	TenantConfig        TenantConfig
	EnvConfig           EnvConfig
	CreatedERunConfig   bool
	CreatedTenantConfig bool
	CreatedEnvConfig    bool
}

type BootstrapInitDependencies struct {
	Store                     BootstrapStore
	FindProjectRoot           ProjectFinderFunc
	GetWorkingDir             WorkDirFunc
	SelectTenant              SelectTenantFunc
	Confirm                   ConfirmFunc
	PromptKubernetesContext   PromptValueFunc
	PromptContainerRegistry   PromptValueFunc
	PromptRemoteRepositoryURL PromptValueFunc
	PromptCodeCommitSSHKeyID  PromptValueFunc
	EnsureKubernetesNamespace NamespaceEnsurerFunc
	LoadProjectConfig         ProjectConfigLoaderFunc
	SaveProjectConfig         ProjectConfigSaverFunc
	WaitForRemoteRuntime      RemoteRuntimeWaitFunc
	RunRemoteCommand          RemoteCommandRunnerFunc
	DeployHelmChart           HelmChartDeployerFunc
	Sleep                     SleepFunc
	Context                   Context
}

type bootstrapRunner struct {
	BootstrapInitDependencies
	Context Context
}

func RunBootstrapInit(ctx Context, params BootstrapInitParams, store BootstrapStore, findProjectRoot ProjectFinderFunc, getWorkingDir WorkDirFunc, selectTenant SelectTenantFunc, confirm ConfirmFunc, promptKubernetesContext PromptValueFunc, promptContainerRegistry PromptValueFunc, ensureKubernetesNamespace NamespaceEnsurerFunc, loadProjectConfig ProjectConfigLoaderFunc, saveProjectConfig ProjectConfigSaverFunc) (BootstrapInitResult, error) {
	return RunBootstrapInitWithDependencies(BootstrapInitDependencies{
		Store:                     store,
		FindProjectRoot:           findProjectRoot,
		GetWorkingDir:             getWorkingDir,
		SelectTenant:              selectTenant,
		Confirm:                   confirm,
		PromptKubernetesContext:   promptKubernetesContext,
		PromptContainerRegistry:   promptContainerRegistry,
		EnsureKubernetesNamespace: ensureKubernetesNamespace,
		LoadProjectConfig:         loadProjectConfig,
		SaveProjectConfig:         saveProjectConfig,
		Context:                   ctx,
	}, params)
}

func RunBootstrapInitWithDependencies(deps BootstrapInitDependencies, params BootstrapInitParams) (BootstrapInitResult, error) {
	return bootstrapRunner{
		BootstrapInitDependencies: deps,
		Context:                   deps.Context,
	}.run(params)
}

func TraceBootstrapStore(ctx Context, store BootstrapStore) BootstrapStore {
	if store == nil {
		store = ConfigStore{}
	}
	return tracedBootstrapStore{
		BootstrapStore: store,
		ctx:            ctx,
	}
}

func TraceProjectConfigSaver(ctx Context, save ProjectConfigSaverFunc) ProjectConfigSaverFunc {
	if save == nil {
		save = SaveProjectConfig
	}
	return func(projectRoot string, config ProjectConfig) error {
		if strings.TrimSpace(projectRoot) == "" {
			return ErrNotInGitRepository
		}
		configPath := filepath.Join(filepath.Clean(projectRoot), ".erun", "config.yaml")
		if err := traceYAMLWrite(ctx, configPath, config); err != nil {
			return err
		}
		if ctx.DryRun {
			return nil
		}
		return save(projectRoot, config)
	}
}

func TraceNamespaceEnsurer(ctx Context, ensure NamespaceEnsurerFunc) NamespaceEnsurerFunc {
	if ensure == nil {
		return nil
	}
	return func(contextName, namespace string) error {
		TraceEnsureKubernetesNamespace(ctx, contextName, namespace)
		if ctx.DryRun {
			return nil
		}
		return ensure(contextName, namespace)
	}
}

func traceYAMLWrite(ctx Context, path string, value any) error {
	if _, err := yaml.Marshal(value); err != nil {
		return ErrFailedToSaveConfig
	}
	ctx.TraceCommand("", "mkdir", "-p", filepath.Dir(path))
	ctx.TraceCommand("", "write-yaml", path)
	return nil
}

type tracedBootstrapStore struct {
	BootstrapStore
	ctx Context
}

func (s tracedBootstrapStore) SaveERunConfig(config ERunConfig) error {
	configPath, err := xdg.ConfigFile(filepath.Join("erun", "config.yaml"))
	if err != nil {
		return ErrNoUserDataFolder
	}
	if err := traceYAMLWrite(s.ctx, configPath, config); err != nil {
		return err
	}
	if s.ctx.DryRun {
		return nil
	}
	return s.BootstrapStore.SaveERunConfig(config)
}

func (s tracedBootstrapStore) SaveTenantConfig(config TenantConfig) error {
	configPath, err := xdg.ConfigFile(filepath.Join("erun", config.Name, "config.yaml"))
	if err != nil {
		return ErrNoUserDataFolder
	}
	if err := traceYAMLWrite(s.ctx, configPath, config); err != nil {
		return err
	}
	if s.ctx.DryRun {
		return nil
	}
	return s.BootstrapStore.SaveTenantConfig(config)
}

func (s tracedBootstrapStore) SaveEnvConfig(tenant string, config EnvConfig) error {
	configPath, err := xdg.ConfigFile(filepath.Join("erun", tenant, config.Name, "config.yaml"))
	if err != nil {
		return ErrNoUserDataFolder
	}
	if err := traceYAMLWrite(s.ctx, configPath, config); err != nil {
		return err
	}
	if s.ctx.DryRun {
		return nil
	}
	return s.BootstrapStore.SaveEnvConfig(tenant, config)
}

func (s tracedBootstrapStore) ListEnvConfigs(tenant string) ([]EnvConfig, error) {
	portStore, ok := s.BootstrapStore.(interface {
		ListEnvConfigs(string) ([]EnvConfig, error)
	})
	if !ok {
		return nil, ErrNotInitialized
	}
	return portStore.ListEnvConfigs(tenant)
}

func (s bootstrapRunner) run(params BootstrapInitParams) (BootstrapInitResult, error) {
	s = s.withDefaults()
	params = normalizeBootstrapParams(params)
	remoteMode := params.Remote

	var result BootstrapInitResult
	var detected projectContext
	var tenants []TenantConfig
	var tenantsLoaded bool
	var setDefaultTenant bool
	var defaultTenantResolved bool

	if remoteMode {
		if params.InitializeCurrentProject || params.ResolveTenant {
			return result, fmt.Errorf("remote initialization requires an explicit tenant")
		}
		if params.Tenant == "" {
			return result, fmt.Errorf("tenant is required with --remote")
		}
		if params.Environment == "" {
			return result, fmt.Errorf("environment is required with --remote")
		}
	}

	findProject := func() (projectContext, error) {
		if detected.loaded {
			return detected, nil
		}
		tenant, root, err := s.FindProjectRoot()
		if err != nil {
			return projectContext{}, err
		}
		detected = projectContext{
			tenant: tenant,
			root:   root,
			loaded: true,
		}
		return detected, nil
	}

	detectProject := func() (projectContext, error) {
		s.Context.Trace("Trying to detect current project directory")
		project, err := findProject()
		if err != nil {
			if errors.Is(err, ErrNotInGitRepository) {
				s.Context.Logger.Error("erun config is not initialized. Run erun in project directory.")
				return projectContext{}, err
			}
			return projectContext{}, err
		}
		return project, nil
	}

	loadTenants := func() ([]TenantConfig, error) {
		if tenantsLoaded {
			return tenants, nil
		}
		loadedTenants, err := s.Store.ListTenantConfigs()
		if err != nil {
			return nil, err
		}
		tenants = loadedTenants
		tenantsLoaded = true
		return tenants, nil
	}

	resolveDefaultTenant := func(tenant, projectRoot string) error {
		if defaultTenantResolved {
			return nil
		}
		updateDefaultTenant, err := s.confirmTenant(params, tenant, projectRoot)
		if err != nil {
			return err
		}
		setDefaultTenant = updateDefaultTenant
		defaultTenantResolved = true
		return nil
	}

	toolConfig, configPath, err := s.Store.LoadERunConfig()
	toolConfigMissing := false
	s.Context.Trace("Loading erun tool configuration, configPath=" + configPath)
	switch {
	case err == nil:
	case errors.Is(err, ErrNotInitialized):
		toolConfigMissing = true
		if params.ResolveTenant {
			break
		}
		tenant := params.Tenant
		projectRoot := params.ProjectRoot
		if remoteMode {
			projectRoot = RemoteWorktreePathForRepoName(tenant)
		} else if tenant == "" || projectRoot == "" {
			project, detectErr := detectProject()
			if detectErr != nil {
				return result, detectErr
			}
			if tenant == "" {
				tenant = project.tenant
			}
			if projectRoot == "" {
				projectRoot = project.root
			}
		}

		if err := resolveDefaultTenant(tenant, projectRoot); err != nil {
			return result, err
		}

		if setDefaultTenant {
			s.Context.Trace("Saving default config")
			toolConfig = ERunConfig{DefaultTenant: tenant}
			if err := s.Store.SaveERunConfig(toolConfig); err != nil {
				return result, err
			}
			result.CreatedERunConfig = true
			toolConfigMissing = false
		}
	case err != nil:
		return result, err
	}
	s.Context.Trace("Loaded erun tool configuration")

	tenant := params.Tenant
	if tenant == "" && !remoteMode {
		project, detectErr := findProject()
		switch {
		case detectErr == nil:
			tenant = project.tenant
		case errors.Is(detectErr, ErrNotInGitRepository):
		case detectErr != nil:
			return result, detectErr
		}
	}
	if tenant == "" && params.ResolveTenant {
		loadedTenants, err := loadTenants()
		if err != nil {
			return result, err
		}
		if len(loadedTenants) > 0 {
			workingDir, err := s.GetWorkingDir()
			if err != nil {
				return result, err
			}
			if currentTenant, found := findTenantForDirectory(workingDir, loadedTenants); found {
				tenant = currentTenant.Name
			}
		}
	}
	if tenant == "" && !remoteMode {
		tenant = toolConfig.DefaultTenant
	}
	if tenant == "" && params.ResolveTenant {
		loadedTenants, err := loadTenants()
		if err != nil {
			return result, err
		}
		if len(loadedTenants) > 0 {
			selection, err := s.selectTenant(params, loadedTenants)
			if err != nil {
				return result, err
			}
			if !selection.Initialize {
				tenant = selection.Tenant
			}
		}
	}
	if tenant == "" && !remoteMode {
		project, detectErr := detectProject()
		if detectErr != nil {
			return result, detectErr
		}
		tenant = project.tenant
	}
	if remoteMode {
		params.ProjectRoot = RemoteWorktreePathForRepoName(tenant)
	}

	s.Context.Trace("Loading tenant configuration")
	tenantConfig, _, err := s.Store.LoadTenantConfig(tenant)
	tenantConfigChanged := false
	switch {
	case err == nil:
	case errors.Is(err, ErrNotInitialized):
		projectRoot := params.ProjectRoot
		if projectRoot == "" && !remoteMode {
			project, detectErr := detectProject()
			if detectErr != nil {
				return result, detectErr
			}
			projectRoot = project.root
		}

		if !defaultTenantResolved {
			if err := resolveDefaultTenant(tenant, projectRoot); err != nil {
				return result, err
			}
		}

		defaultEnvironment := params.Environment
		if defaultEnvironment == "" {
			defaultEnvironment = DefaultEnvironment
		}

		s.Context.Trace("Adding new tenant")
		tenantConfig = TenantConfig{
			Name:               tenant,
			ProjectRoot:        projectRoot,
			DefaultEnvironment: defaultEnvironment,
		}
		if err := s.Store.SaveTenantConfig(tenantConfig); err != nil {
			return result, err
		}
		result.CreatedTenantConfig = true
	case err != nil:
		return result, err
	}

	if tenantConfig.Name == "" {
		tenantConfig.Name = tenant
		tenantConfigChanged = true
	}
	if remoteMode {
		if tenantConfig.ProjectRoot != params.ProjectRoot {
			tenantConfig.ProjectRoot = params.ProjectRoot
			tenantConfigChanged = true
		}
	}
	s.Context.Trace("Loaded tenant configuration")

	envName := params.Environment
	if envName == "" {
		envName = tenantConfig.DefaultEnvironment
	}
	if envName == "" {
		envName = DefaultEnvironment
	}
	if tenantConfig.DefaultEnvironment == "" {
		tenantConfig.DefaultEnvironment = envName
		tenantConfigChanged = true
	}
	if tenantConfigChanged {
		if err := s.Store.SaveTenantConfig(tenantConfig); err != nil {
			return result, err
		}
	}

	s.Context.Trace("Loading environment configuration")
	envConfig, _, err := s.Store.LoadEnvConfig(tenant, envName)
	envConfigChanged := false
	switch {
	case err == nil:
	case errors.Is(err, ErrNotInitialized):
		envProjectRoot := params.ProjectRoot
		if envProjectRoot == "" {
			envProjectRoot = tenantConfig.ProjectRoot
		}
		if envProjectRoot == "" && !remoteMode {
			project, detectErr := findProject()
			if detectErr == nil {
				envProjectRoot = project.root
			} else if !errors.Is(detectErr, ErrNotInGitRepository) {
				return result, detectErr
			}
		}

		if err := s.confirmEnvironment(params, tenant, envName); err != nil {
			return result, err
		}

		kubernetesContext, err := s.resolveKubernetesContext(params, tenant, envName, "")
		if err != nil {
			return result, err
		}
		if err := s.ensureKubernetesNamespace(tenant, envName, "", kubernetesContext); err != nil {
			return result, err
		}
		cloudProviderAlias, err := s.resolveCloudProviderAlias(kubernetesContext, "")
		if err != nil {
			return result, err
		}
		managedCloud, err := managedCloudEnvironment(s.Store, EnvConfig{
			KubernetesContext:  kubernetesContext,
			CloudProviderAlias: cloudProviderAlias,
			Remote:             remoteMode,
		})
		if err != nil {
			return result, err
		}
		containerRegistry, err := s.resolveContainerRegistry(params, tenant, envName, envProjectRoot, "", true)
		if err != nil {
			return result, err
		}
		if err := s.saveProjectContainerRegistry(envProjectRoot, envName, containerRegistry, remoteMode); err != nil {
			return result, err
		}

		s.Context.Trace("Adding new environment")
		envConfig = EnvConfig{
			Name:               envName,
			RepoPath:           envProjectRoot,
			KubernetesContext:  kubernetesContext,
			CloudProviderAlias: cloudProviderAlias,
			ManagedCloud:       managedCloud,
			RuntimeVersion:     strings.TrimSpace(params.RuntimeVersion),
			Remote:             remoteMode,
		}
		if err := saveEnvConfig(s.Store, tenant, envConfig); err != nil {
			return result, err
		}
		envConfig.ContainerRegistry = containerRegistry
		if remoteMode && containerRegistry != "" {
			envConfigChanged = true
		}
		result.CreatedEnvConfig = true
	case err != nil:
		return result, err
	}
	if remoteMode {
		if envConfig.RepoPath != params.ProjectRoot {
			envConfig.RepoPath = params.ProjectRoot
			envConfigChanged = true
		}
		if runtimeVersion := strings.TrimSpace(params.RuntimeVersion); runtimeVersion != "" && envConfig.RuntimeVersion != runtimeVersion {
			envConfig.RuntimeVersion = runtimeVersion
			envConfigChanged = true
		}
		if !envConfig.Remote {
			envConfig.Remote = true
			envConfigChanged = true
		}
	}

	kubernetesContext, err := s.resolveKubernetesContext(params, tenant, envName, envConfig.KubernetesContext)
	if err != nil {
		return result, err
	}
	if kubernetesContext != envConfig.KubernetesContext {
		if err := s.ensureKubernetesNamespace(tenant, envName, envConfig.KubernetesContext, kubernetesContext); err != nil {
			return result, err
		}
		envConfig.KubernetesContext = kubernetesContext
		envConfigChanged = true
	}
	cloudProviderAlias, err := s.resolveCloudProviderAlias(kubernetesContext, envConfig.CloudProviderAlias)
	if err != nil {
		return result, err
	}
	if cloudProviderAlias != envConfig.CloudProviderAlias {
		envConfig.CloudProviderAlias = cloudProviderAlias
		envConfigChanged = true
	}
	managedCloud, err := managedCloudEnvironment(s.Store, envConfig)
	if err != nil {
		return result, err
	}
	if managedCloud != envConfig.ManagedCloud {
		envConfig.ManagedCloud = managedCloud
		envConfigChanged = true
	}
	projectRoot := envConfig.RepoPath
	if projectRoot == "" {
		projectRoot = tenantConfig.ProjectRoot
	}
	containerRegistry, err := s.resolveContainerRegistry(params, tenant, envName, projectRoot, envConfig.ContainerRegistry, false)
	if err != nil {
		return result, err
	}
	if containerRegistry != "" {
		if err := s.saveProjectContainerRegistry(projectRoot, envName, containerRegistry, remoteMode); err != nil {
			return result, err
		}
	}
	if containerRegistry != "" && containerRegistry != envConfig.ContainerRegistry {
		envConfig.ContainerRegistry = containerRegistry
		envConfigChanged = true
	}
	if remoteMode {
		req, err := s.ensureRemoteRepository(params, tenant, envName, kubernetesContext, projectRoot)
		if err != nil {
			return result, err
		}
		if params.Bootstrap {
			if err := s.ensureRemoteDefaultDevopsBootstrap(req, projectRoot, tenant, envName, params.RuntimeVersion); err != nil {
				return result, err
			}
		}
	} else {
		if err := EnsureDefaultDevopsModuleWithVersion(s.Context, projectRoot, tenant, params.RuntimeVersion); err != nil {
			return result, err
		}
		if err := EnsureDefaultDevopsChart(s.Context, projectRoot, tenant, envName); err != nil {
			return result, err
		}
	}
	if envConfigChanged {
		if err := saveEnvConfig(s.Store, tenant, envConfig); err != nil {
			return result, err
		}
	}

	if setDefaultTenant && (toolConfigMissing || toolConfig.DefaultTenant != tenant) {
		s.Context.Trace("Saving default config")
		toolConfig.DefaultTenant = tenant
		if err := s.Store.SaveERunConfig(toolConfig); err != nil {
			return result, err
		}
		if toolConfigMissing {
			result.CreatedERunConfig = true
		}
	}

	result.ERunConfig = toolConfig
	result.TenantConfig = tenantConfig
	result.EnvConfig = envConfig
	s.Context.Trace("Configuration initialized OK")
	return result, nil
}

func tenantConfirmationLabel(tenant, projectRoot string) string {
	return fmt.Sprintf("Initialize tenant %q (path: %s) as the default tenant", tenant, projectRoot)
}

func environmentConfirmationLabel(tenant, envName string) string {
	return fmt.Sprintf("Initialize default environment %q for tenant %q", envName, tenant)
}

func kubernetesContextLabel(tenant, envName string) string {
	return fmt.Sprintf("Kubernetes context for environment %q in tenant %q", envName, tenant)
}

func containerRegistryLabel(tenant, envName string) string {
	return fmt.Sprintf("Container registry for environment %q in tenant %q", envName, tenant)
}

func remoteRepositoryLabel(tenant, envName string) string {
	return fmt.Sprintf("Git remote URL for environment %q in tenant %q", envName, tenant)
}

func codeCommitSSHKeyIDLabel(tenant, envName string) string {
	return fmt.Sprintf("CodeCommit SSH public key ID for environment %q in tenant %q", envName, tenant)
}

func remoteKeyImportLabel(tenant, envName string) string {
	return fmt.Sprintf("Import the SSH public key above for environment %q in tenant %q and continue", envName, tenant)
}

func remoteHostConfigLabel(tenant, envName string) string {
	return fmt.Sprintf("Use existing SSH host config for environment %q in tenant %q", envName, tenant)
}

func KubernetesNamespaceName(tenant, envName string) string {
	return normalizeNamespaceName(tenant + "-" + envName)
}

func normalizeBootstrapParams(params BootstrapInitParams) BootstrapInitParams {
	params.Tenant = strings.TrimSpace(params.Tenant)
	params.SelectedTenant = strings.TrimSpace(params.SelectedTenant)
	params.ProjectRoot = strings.TrimSpace(params.ProjectRoot)
	params.Environment = strings.TrimSpace(params.Environment)
	params.RuntimeVersion = strings.TrimSpace(params.RuntimeVersion)
	params.RuntimeImage = strings.TrimSpace(params.RuntimeImage)
	params.KubernetesContext = strings.TrimSpace(params.KubernetesContext)
	params.ContainerRegistry = strings.TrimSpace(params.ContainerRegistry)
	params.RemoteRepositoryURL = strings.TrimSpace(params.RemoteRepositoryURL)
	params.CodeCommitSSHKeyID = strings.TrimSpace(params.CodeCommitSSHKeyID)
	return params
}

func (s bootstrapRunner) withDefaults() bootstrapRunner {
	s = s.withStoreDefaults()
	s = s.withRuntimeDefaults()
	return s.withLoggerDefaults()
}

func (s bootstrapRunner) withStoreDefaults() bootstrapRunner {
	if s.Store == nil {
		s.Store = ConfigStore{}
	}
	if s.FindProjectRoot == nil {
		s.FindProjectRoot = FindProjectRoot
	}
	if s.GetWorkingDir == nil {
		s.GetWorkingDir = os.Getwd
	}
	if s.LoadProjectConfig == nil {
		s.LoadProjectConfig = LoadProjectConfig
	}
	if s.SaveProjectConfig == nil {
		s.SaveProjectConfig = SaveProjectConfig
	}
	return s
}

func (s bootstrapRunner) withRuntimeDefaults() bootstrapRunner {
	if s.WaitForRemoteRuntime == nil {
		s.WaitForRemoteRuntime = WaitForShellDeployment
	}
	if s.RunRemoteCommand == nil {
		s.RunRemoteCommand = RunRemoteCommand
	}
	if s.DeployHelmChart == nil {
		s.DeployHelmChart = DeployHelmChart
	}
	if s.Sleep == nil {
		s.Sleep = time.Sleep
	}
	return s
}

func (s bootstrapRunner) withLoggerDefaults() bootstrapRunner {
	if s.Context.Logger.verbosity == 0 && s.Context.Logger.stdout == nil && s.Context.Logger.stderr == nil {
		s.Context.Logger = NewLoggerWithWriters(-1, io.Discard, io.Discard)
	}
	return s
}

func (s bootstrapRunner) confirmTenant(params BootstrapInitParams, tenant, projectRoot string) (bool, error) {
	if params.AutoApprove {
		return true, nil
	}
	if params.ConfirmTenant != nil {
		return *params.ConfirmTenant, nil
	}
	return s.confirm(BootstrapInitInteraction{
		Type:  BootstrapInitInteractionConfirmTenant,
		Label: tenantConfirmationLabel(tenant, projectRoot),
	})
}

func (s bootstrapRunner) confirmEnvironment(params BootstrapInitParams, tenant, envName string) error {
	if params.AutoApprove {
		return nil
	}
	if params.ConfirmEnvironment != nil {
		if *params.ConfirmEnvironment {
			return nil
		}
		return ErrEnvironmentInitializationCancelled
	}
	confirmed, err := s.confirm(BootstrapInitInteraction{
		Type:  BootstrapInitInteractionConfirmEnvironment,
		Label: environmentConfirmationLabel(tenant, envName),
	})
	if err != nil {
		return err
	}
	if !confirmed {
		return ErrEnvironmentInitializationCancelled
	}
	return nil
}

func (s bootstrapRunner) confirm(interaction BootstrapInitInteraction) (bool, error) {
	if s.Confirm == nil {
		return false, BootstrapInitInteractionError{Interaction: interaction}
	}
	confirmed, err := s.Confirm(interaction.Label)
	if err != nil {
		return false, err
	}
	return confirmed, nil
}

func (s bootstrapRunner) resolveKubernetesContext(params BootstrapInitParams, tenant, envName, current string) (string, error) {
	if params.KubernetesContext != "" {
		return params.KubernetesContext, nil
	}

	current = strings.TrimSpace(current)
	if current != "" || params.AutoApprove {
		return current, nil
	}

	if s.PromptKubernetesContext == nil {
		return "", BootstrapInitInteractionError{Interaction: BootstrapInitInteraction{
			Type:  BootstrapInitInteractionKubernetesContext,
			Label: kubernetesContextLabel(tenant, envName),
		}}
	}

	context, err := s.PromptKubernetesContext(kubernetesContextLabel(tenant, envName))
	if err != nil {
		return "", err
	}
	context = strings.TrimSpace(context)
	if context == "" {
		return "", ErrKubernetesContextCancelled
	}
	return context, nil
}

func (s bootstrapRunner) resolveContainerRegistry(params BootstrapInitParams, tenant, envName, projectRoot, current string, creating bool) (string, error) {
	if params.ContainerRegistry != "" {
		return params.ContainerRegistry, nil
	}

	if projectRoot != "" && s.LoadProjectConfig != nil {
		projectConfig, _, err := s.LoadProjectConfig(projectRoot)
		if err != nil && !errors.Is(err, ErrNotInitialized) {
			return "", err
		}
		if err == nil {
			projectRegistry := projectConfig.ContainerRegistryForEnvironment(envName)
			if projectRegistry != "" {
				return projectRegistry, nil
			}
		}
	}

	current = strings.TrimSpace(current)
	if current != "" {
		return current, nil
	}
	if !creating {
		return "", nil
	}
	if params.AutoApprove {
		return DefaultContainerRegistry, nil
	}
	if s.PromptContainerRegistry == nil {
		return "", BootstrapInitInteractionError{Interaction: BootstrapInitInteraction{
			Type:         BootstrapInitInteractionContainerRegistry,
			Label:        containerRegistryLabel(tenant, envName),
			DefaultValue: DefaultContainerRegistry,
		}}
	}

	registry, err := s.PromptContainerRegistry(containerRegistryLabel(tenant, envName))
	if err != nil {
		return "", err
	}
	registry = strings.TrimSpace(registry)
	if registry == "" {
		return DefaultContainerRegistry, nil
	}
	return registry, nil
}

func (s bootstrapRunner) resolveCloudProviderAlias(kubernetesContext, current string) (string, error) {
	current = strings.TrimSpace(current)
	if current != "" {
		return current, nil
	}

	status, ok, err := findCloudContextForKubernetesContext(s.Store, kubernetesContext)
	if err != nil || !ok {
		return current, err
	}
	return strings.TrimSpace(status.CloudProviderAlias), nil
}

func (s bootstrapRunner) saveProjectContainerRegistry(projectRoot, envName, registry string, remote bool) error {
	if remote {
		return nil
	}
	if projectRoot == "" || envName == "" || registry == "" || s.SaveProjectConfig == nil {
		return nil
	}

	projectConfig := ProjectConfig{}
	if s.LoadProjectConfig != nil {
		loadedConfig, _, err := s.LoadProjectConfig(projectRoot)
		if err != nil && !errors.Is(err, ErrNotInitialized) {
			return err
		}
		if err == nil {
			projectConfig = loadedConfig
		}
	}

	if projectConfig.ContainerRegistryForEnvironment(envName) == registry {
		return nil
	}

	projectConfig.SetContainerRegistryForEnvironment(envName, registry)
	return s.SaveProjectConfig(projectRoot, projectConfig)
}

func (s bootstrapRunner) ensureKubernetesNamespace(tenant, envName, currentContext, nextContext string) error {
	if s.EnsureKubernetesNamespace == nil {
		return nil
	}

	nextContext = strings.TrimSpace(nextContext)
	if nextContext == "" || nextContext == strings.TrimSpace(currentContext) {
		return nil
	}
	if err := s.Context.EnsureKubernetesContext(nextContext); err != nil {
		return err
	}

	namespace := KubernetesNamespaceName(tenant, envName)
	if namespace == "" {
		return fmt.Errorf("kubernetes namespace name is empty for tenant %q and environment %q", tenant, envName)
	}

	return s.EnsureKubernetesNamespace(nextContext, namespace)
}

func (s bootstrapRunner) selectTenant(params BootstrapInitParams, tenants []TenantConfig) (TenantSelectionResult, error) {
	if params.InitializeCurrentProject {
		return TenantSelectionResult{Initialize: true}, nil
	}
	if params.SelectedTenant != "" {
		return TenantSelectionResult{Tenant: params.SelectedTenant}, nil
	}
	if s.SelectTenant == nil {
		options := make([]string, 0, len(tenants)+1)
		for _, tenant := range tenants {
			options = append(options, tenant.Name)
		}
		options = append(options, InitializeCurrentProject)
		return TenantSelectionResult{}, BootstrapInitInteractionError{Interaction: BootstrapInitInteraction{
			Type:    BootstrapInitInteractionSelectTenant,
			Label:   "Select tenant",
			Options: options,
		}}
	}
	selection, err := s.SelectTenant(tenants)
	if err != nil {
		return TenantSelectionResult{}, err
	}
	if selection.Initialize {
		return selection, nil
	}
	if selection.Tenant == "" {
		return TenantSelectionResult{}, ErrTenantSelectionCancelled
	}
	return selection, nil
}

func findTenantForDirectory(dir string, tenants []TenantConfig) (TenantConfig, bool) {
	cleanDir := filepath.Clean(dir)
	bestIndex := -1

	for i, tenant := range tenants {
		if tenant.ProjectRoot == "" {
			continue
		}
		if !isWithinDirectory(cleanDir, filepath.Clean(tenant.ProjectRoot)) {
			continue
		}
		if bestIndex == -1 || len(tenant.ProjectRoot) > len(tenants[bestIndex].ProjectRoot) {
			bestIndex = i
		}
	}

	if bestIndex == -1 {
		return TenantConfig{}, false
	}
	return tenants[bestIndex], true
}

func isWithinDirectory(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func normalizeNamespaceName(value string) string {
	var builder strings.Builder
	lastHyphen := false

	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastHyphen = false
		case !lastHyphen && builder.Len() > 0:
			builder.WriteByte('-')
			lastHyphen = true
		}
	}

	result := strings.Trim(builder.String(), "-")
	if len(result) > 63 {
		result = strings.Trim(result[:63], "-")
	}
	return result
}

func saveEnvConfig(store BootstrapStore, tenant string, config EnvConfig) error {
	stored := config
	if !stored.Remote {
		stored.ContainerRegistry = ""
	}
	return store.SaveEnvConfig(tenant, stored)
}

type projectContext struct {
	tenant string
	root   string
	loaded bool
}
