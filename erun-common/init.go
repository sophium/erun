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

type bootstrapRunState struct {
	runner                bootstrapRunner
	params                BootstrapInitParams
	result                BootstrapInitResult
	detected              projectContext
	tenants               []TenantConfig
	tenantsLoaded         bool
	setDefaultTenant      bool
	defaultTenantResolved bool
	toolConfig            ERunConfig
	toolConfigMissing     bool
	tenant                string
	tenantConfig          TenantConfig
	tenantConfigChanged   bool
	envName               string
	envConfig             EnvConfig
	envConfigChanged      bool
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
	state := bootstrapRunState{
		runner: s,
		params: normalizeBootstrapParams(params),
	}
	if err := state.run(); err != nil {
		return state.result, err
	}
	return state.result, nil
}

func (s *bootstrapRunState) run() error {
	if err := s.validateRemoteParams(); err != nil {
		return err
	}
	if err := s.loadBootstrapConfigs(); err != nil {
		return err
	}
	if err := s.applyBootstrapConfigChanges(); err != nil {
		return err
	}
	s.finish()
	return nil
}

func (s *bootstrapRunState) loadBootstrapConfigs() error {
	if err := s.loadToolConfig(); err != nil {
		return err
	}
	if err := s.resolveTenant(); err != nil {
		return err
	}
	if err := s.loadTenantConfig(); err != nil {
		return err
	}
	if err := s.resolveEnvironmentName(); err != nil {
		return err
	}
	return s.loadEnvConfig()
}

func (s *bootstrapRunState) applyBootstrapConfigChanges() error {
	if err := s.updateEnvConfig(); err != nil {
		return err
	}
	if err := s.ensureDevopsAssets(); err != nil {
		return err
	}
	if err := s.saveEnvConfigIfChanged(); err != nil {
		return err
	}
	return s.saveDefaultTenantIfNeeded()
}

func (s *bootstrapRunState) validateRemoteParams() error {
	if !s.params.Remote {
		return nil
	}
	if s.params.InitializeCurrentProject || s.params.ResolveTenant {
		return fmt.Errorf("remote initialization requires an explicit tenant")
	}
	if s.params.Tenant == "" {
		return fmt.Errorf("tenant is required with --remote")
	}
	if s.params.Environment == "" {
		return fmt.Errorf("environment is required with --remote")
	}
	return nil
}

func (s *bootstrapRunState) findProject() (projectContext, error) {
	if s.detected.loaded {
		return s.detected, nil
	}
	tenant, root, err := s.runner.FindProjectRoot()
	if err != nil {
		return projectContext{}, err
	}
	s.detected = projectContext{
		tenant: tenant,
		root:   root,
		loaded: true,
	}
	return s.detected, nil
}

func (s *bootstrapRunState) detectProject() (projectContext, error) {
	s.runner.Context.Trace("Trying to detect current project directory")
	project, err := s.findProject()
	if err == nil {
		return project, nil
	}
	if errors.Is(err, ErrNotInGitRepository) {
		s.runner.Context.Logger.Error("erun config is not initialized. Run erun in project directory.")
	}
	return projectContext{}, err
}

func (s *bootstrapRunState) loadTenants() ([]TenantConfig, error) {
	if s.tenantsLoaded {
		return s.tenants, nil
	}
	tenants, err := s.runner.Store.ListTenantConfigs()
	if err != nil {
		return nil, err
	}
	s.tenants = tenants
	s.tenantsLoaded = true
	return s.tenants, nil
}

func (s *bootstrapRunState) resolveDefaultTenant(tenant, projectRoot string) error {
	if s.defaultTenantResolved {
		return nil
	}
	updateDefaultTenant, err := s.runner.confirmTenant(s.params, tenant, projectRoot)
	if err != nil {
		return err
	}
	s.setDefaultTenant = updateDefaultTenant
	s.defaultTenantResolved = true
	return nil
}

func (s *bootstrapRunState) loadToolConfig() error {
	toolConfig, configPath, err := s.runner.Store.LoadERunConfig()
	s.toolConfig = toolConfig
	s.runner.Context.Trace("Loading erun tool configuration, configPath=" + configPath)
	switch {
	case err == nil:
	case errors.Is(err, ErrNotInitialized):
		if err := s.initializeMissingToolConfig(); err != nil {
			return err
		}
	case err != nil:
		return err
	}
	s.runner.Context.Trace("Loaded erun tool configuration")
	return nil
}

func (s *bootstrapRunState) initializeMissingToolConfig() error {
	s.toolConfigMissing = true
	if s.params.ResolveTenant {
		return nil
	}
	tenant, projectRoot, err := s.defaultTenantCandidate()
	if err != nil {
		return err
	}
	if err := s.resolveDefaultTenant(tenant, projectRoot); err != nil {
		return err
	}
	if !s.setDefaultTenant {
		return nil
	}
	s.runner.Context.Trace("Saving default config")
	s.toolConfig = ERunConfig{DefaultTenant: tenant}
	if err := s.runner.Store.SaveERunConfig(s.toolConfig); err != nil {
		return err
	}
	s.result.CreatedERunConfig = true
	s.toolConfigMissing = false
	return nil
}

func (s *bootstrapRunState) defaultTenantCandidate() (string, string, error) {
	tenant := s.params.Tenant
	projectRoot := s.params.ProjectRoot
	if s.params.Remote {
		return tenant, RemoteWorktreePathForRepoName(tenant), nil
	}
	if tenant != "" && projectRoot != "" {
		return tenant, projectRoot, nil
	}
	project, err := s.detectProject()
	if err != nil {
		return "", "", err
	}
	if tenant == "" {
		tenant = project.tenant
	}
	if projectRoot == "" {
		projectRoot = project.root
	}
	return tenant, projectRoot, nil
}

func (s *bootstrapRunState) resolveTenant() error {
	s.tenant = s.params.Tenant
	if err := s.resolveTenantFromProject(); err != nil {
		return err
	}
	if err := s.resolveTenantFromDirectory(); err != nil {
		return err
	}
	if s.tenant == "" && !s.params.Remote {
		s.tenant = s.toolConfig.DefaultTenant
	}
	if err := s.resolveTenantFromSelection(); err != nil {
		return err
	}
	if s.tenant == "" && !s.params.Remote {
		project, err := s.detectProject()
		if err != nil {
			return err
		}
		s.tenant = project.tenant
	}
	if s.params.Remote {
		s.params.ProjectRoot = RemoteWorktreePathForRepoName(s.tenant)
	}
	return nil
}

func (s *bootstrapRunState) resolveTenantFromProject() error {
	if s.tenant != "" || s.params.Remote {
		return nil
	}
	project, err := s.findProject()
	switch {
	case err == nil:
		s.tenant = project.tenant
	case errors.Is(err, ErrNotInGitRepository):
	case err != nil:
		return err
	}
	return nil
}

func (s *bootstrapRunState) resolveTenantFromDirectory() error {
	if s.tenant != "" || !s.params.ResolveTenant {
		return nil
	}
	tenants, err := s.loadTenants()
	if err != nil {
		return err
	}
	if len(tenants) == 0 {
		return nil
	}
	workingDir, err := s.runner.GetWorkingDir()
	if err != nil {
		return err
	}
	if currentTenant, found := findTenantForDirectory(workingDir, tenants); found {
		s.tenant = currentTenant.Name
	}
	return nil
}

func (s *bootstrapRunState) resolveTenantFromSelection() error {
	if s.tenant != "" || !s.params.ResolveTenant {
		return nil
	}
	tenants, err := s.loadTenants()
	if err != nil {
		return err
	}
	if len(tenants) == 0 {
		return nil
	}
	selection, err := s.runner.selectTenant(s.params, tenants)
	if err != nil {
		return err
	}
	if !selection.Initialize {
		s.tenant = selection.Tenant
	}
	return nil
}

func (s *bootstrapRunState) loadTenantConfig() error {
	s.runner.Context.Trace("Loading tenant configuration")
	tenantConfig, _, err := s.runner.Store.LoadTenantConfig(s.tenant)
	switch {
	case err == nil:
		s.tenantConfig = tenantConfig
	case errors.Is(err, ErrNotInitialized):
		if err := s.createTenantConfig(); err != nil {
			return err
		}
	case err != nil:
		return err
	}
	s.normalizeTenantConfig()
	s.runner.Context.Trace("Loaded tenant configuration")
	return nil
}

func (s *bootstrapRunState) createTenantConfig() error {
	projectRoot, err := s.tenantProjectRoot()
	if err != nil {
		return err
	}
	if err := s.resolveDefaultTenant(s.tenant, projectRoot); err != nil {
		return err
	}
	s.runner.Context.Trace("Adding new tenant")
	s.tenantConfig = TenantConfig{
		Name:               s.tenant,
		ProjectRoot:        projectRoot,
		DefaultEnvironment: defaultBootstrapEnvironment(s.params.Environment),
	}
	if err := s.runner.Store.SaveTenantConfig(s.tenantConfig); err != nil {
		return err
	}
	s.result.CreatedTenantConfig = true
	return nil
}

func (s *bootstrapRunState) tenantProjectRoot() (string, error) {
	projectRoot := s.params.ProjectRoot
	if projectRoot != "" || s.params.Remote {
		return projectRoot, nil
	}
	project, err := s.detectProject()
	if err != nil {
		return "", err
	}
	return project.root, nil
}

func defaultBootstrapEnvironment(envName string) string {
	if envName != "" {
		return envName
	}
	return DefaultEnvironment
}

func (s *bootstrapRunState) normalizeTenantConfig() {
	if s.tenantConfig.Name == "" {
		s.tenantConfig.Name = s.tenant
		s.tenantConfigChanged = true
	}
	if s.params.Remote && s.tenantConfig.ProjectRoot != s.params.ProjectRoot {
		s.tenantConfig.ProjectRoot = s.params.ProjectRoot
		s.tenantConfigChanged = true
	}
}

func (s *bootstrapRunState) resolveEnvironmentName() error {
	s.envName = s.params.Environment
	if s.envName == "" {
		s.envName = s.tenantConfig.DefaultEnvironment
	}
	if s.envName == "" {
		s.envName = DefaultEnvironment
	}
	if s.tenantConfig.DefaultEnvironment == "" {
		s.tenantConfig.DefaultEnvironment = s.envName
		s.tenantConfigChanged = true
	}
	if !s.tenantConfigChanged {
		return nil
	}
	return s.runner.Store.SaveTenantConfig(s.tenantConfig)
}

func (s *bootstrapRunState) loadEnvConfig() error {
	s.runner.Context.Trace("Loading environment configuration")
	envConfig, _, err := s.runner.Store.LoadEnvConfig(s.tenant, s.envName)
	switch {
	case err == nil:
		s.envConfig = envConfig
	case errors.Is(err, ErrNotInitialized):
		return s.createEnvConfig()
	case err != nil:
		return err
	}
	return nil
}

func (s *bootstrapRunState) createEnvConfig() error {
	envProjectRoot, err := s.envProjectRoot()
	if err != nil {
		return err
	}
	kubernetesContext, cloudProviderAlias, managedCloud, err := s.resolveNewEnvCloudConfig()
	if err != nil {
		return err
	}
	containerRegistry, err := s.runner.resolveContainerRegistry(s.params, s.tenant, s.envName, envProjectRoot, "", true)
	if err != nil {
		return err
	}
	if err := s.runner.saveProjectContainerRegistry(envProjectRoot, s.envName, containerRegistry, s.params.Remote); err != nil {
		return err
	}
	s.runner.Context.Trace("Adding new environment")
	s.envConfig = EnvConfig{
		Name:               s.envName,
		RepoPath:           envProjectRoot,
		KubernetesContext:  kubernetesContext,
		CloudProviderAlias: cloudProviderAlias,
		ManagedCloud:       managedCloud,
		RuntimeVersion:     strings.TrimSpace(s.params.RuntimeVersion),
		Remote:             s.params.Remote,
	}
	if err := saveEnvConfig(s.runner.Store, s.tenant, s.envConfig); err != nil {
		return err
	}
	s.envConfig.ContainerRegistry = containerRegistry
	s.envConfigChanged = s.params.Remote && containerRegistry != ""
	s.result.CreatedEnvConfig = true
	return nil
}

func (s *bootstrapRunState) envProjectRoot() (string, error) {
	envProjectRoot := s.params.ProjectRoot
	if envProjectRoot == "" {
		envProjectRoot = s.tenantConfig.ProjectRoot
	}
	if envProjectRoot != "" || s.params.Remote {
		return envProjectRoot, nil
	}
	project, err := s.findProject()
	if err == nil {
		return project.root, nil
	}
	if errors.Is(err, ErrNotInGitRepository) {
		return "", nil
	}
	return "", err
}

func (s *bootstrapRunState) resolveNewEnvCloudConfig() (string, string, bool, error) {
	if err := s.runner.confirmEnvironment(s.params, s.tenant, s.envName); err != nil {
		return "", "", false, err
	}
	kubernetesContext, err := s.runner.resolveKubernetesContext(s.params, s.tenant, s.envName, "")
	if err != nil {
		return "", "", false, err
	}
	if err := s.runner.ensureKubernetesNamespace(s.tenant, s.envName, "", kubernetesContext); err != nil {
		return "", "", false, err
	}
	cloudProviderAlias, err := s.runner.resolveCloudProviderAlias(kubernetesContext, "")
	if err != nil {
		return "", "", false, err
	}
	managedCloud, err := managedCloudEnvironment(s.runner.Store, EnvConfig{
		KubernetesContext:  kubernetesContext,
		CloudProviderAlias: cloudProviderAlias,
		Remote:             s.params.Remote,
	})
	return kubernetesContext, cloudProviderAlias, managedCloud, err
}

func (s *bootstrapRunState) updateEnvConfig() error {
	s.updateRemoteEnvConfig()
	kubernetesContext, err := s.updateEnvKubernetesContext()
	if err != nil {
		return err
	}
	if err := s.updateEnvCloudProvider(kubernetesContext); err != nil {
		return err
	}
	return s.updateEnvContainerRegistry()
}

func (s *bootstrapRunState) updateRemoteEnvConfig() {
	if !s.params.Remote {
		return
	}
	if s.envConfig.RepoPath != s.params.ProjectRoot {
		s.envConfig.RepoPath = s.params.ProjectRoot
		s.envConfigChanged = true
	}
	if runtimeVersion := strings.TrimSpace(s.params.RuntimeVersion); runtimeVersion != "" && s.envConfig.RuntimeVersion != runtimeVersion {
		s.envConfig.RuntimeVersion = runtimeVersion
		s.envConfigChanged = true
	}
	if !s.envConfig.Remote {
		s.envConfig.Remote = true
		s.envConfigChanged = true
	}
}

func (s *bootstrapRunState) updateEnvKubernetesContext() (string, error) {
	kubernetesContext, err := s.runner.resolveKubernetesContext(s.params, s.tenant, s.envName, s.envConfig.KubernetesContext)
	if err != nil {
		return "", err
	}
	if kubernetesContext == s.envConfig.KubernetesContext {
		return kubernetesContext, nil
	}
	if err := s.runner.ensureKubernetesNamespace(s.tenant, s.envName, s.envConfig.KubernetesContext, kubernetesContext); err != nil {
		return "", err
	}
	s.envConfig.KubernetesContext = kubernetesContext
	s.envConfigChanged = true
	return kubernetesContext, nil
}

func (s *bootstrapRunState) updateEnvCloudProvider(kubernetesContext string) error {
	cloudProviderAlias, err := s.runner.resolveCloudProviderAlias(kubernetesContext, s.envConfig.CloudProviderAlias)
	if err != nil {
		return err
	}
	if cloudProviderAlias != s.envConfig.CloudProviderAlias {
		s.envConfig.CloudProviderAlias = cloudProviderAlias
		s.envConfigChanged = true
	}
	managedCloud, err := managedCloudEnvironment(s.runner.Store, s.envConfig)
	if err != nil {
		return err
	}
	if managedCloud != s.envConfig.ManagedCloud {
		s.envConfig.ManagedCloud = managedCloud
		s.envConfigChanged = true
	}
	return nil
}

func (s *bootstrapRunState) updateEnvContainerRegistry() error {
	projectRoot := s.projectRoot()
	containerRegistry, err := s.runner.resolveContainerRegistry(s.params, s.tenant, s.envName, projectRoot, s.envConfig.ContainerRegistry, false)
	if err != nil {
		return err
	}
	if containerRegistry == "" {
		return nil
	}
	if err := s.runner.saveProjectContainerRegistry(projectRoot, s.envName, containerRegistry, s.params.Remote); err != nil {
		return err
	}
	if containerRegistry != s.envConfig.ContainerRegistry {
		s.envConfig.ContainerRegistry = containerRegistry
		s.envConfigChanged = true
	}
	return nil
}

func (s *bootstrapRunState) projectRoot() string {
	projectRoot := s.envConfig.RepoPath
	if projectRoot == "" {
		return s.tenantConfig.ProjectRoot
	}
	return projectRoot
}

func (s *bootstrapRunState) ensureDevopsAssets() error {
	projectRoot := s.projectRoot()
	if s.params.Remote {
		return s.ensureRemoteDevopsAssets(projectRoot)
	}
	if err := EnsureDefaultDevopsModuleWithVersion(s.runner.Context, projectRoot, s.tenant, s.params.RuntimeVersion); err != nil {
		return err
	}
	return EnsureDefaultDevopsChart(s.runner.Context, projectRoot, s.tenant, s.envName)
}

func (s *bootstrapRunState) ensureRemoteDevopsAssets(projectRoot string) error {
	req, err := s.runner.ensureRemoteRepository(s.params, s.tenant, s.envName, s.envConfig.KubernetesContext, projectRoot)
	if err != nil {
		return err
	}
	if !s.params.Bootstrap {
		return nil
	}
	return s.runner.ensureRemoteDefaultDevopsBootstrap(req, projectRoot, s.tenant, s.envName, s.params.RuntimeVersion)
}

func (s *bootstrapRunState) saveEnvConfigIfChanged() error {
	if !s.envConfigChanged {
		return nil
	}
	return saveEnvConfig(s.runner.Store, s.tenant, s.envConfig)
}

func (s *bootstrapRunState) saveDefaultTenantIfNeeded() error {
	if !s.setDefaultTenant || (!s.toolConfigMissing && s.toolConfig.DefaultTenant == s.tenant) {
		return nil
	}
	s.runner.Context.Trace("Saving default config")
	s.toolConfig.DefaultTenant = s.tenant
	if err := s.runner.Store.SaveERunConfig(s.toolConfig); err != nil {
		return err
	}
	if s.toolConfigMissing {
		s.result.CreatedERunConfig = true
	}
	return nil
}

func (s *bootstrapRunState) finish() {
	s.result.ERunConfig = s.toolConfig
	s.result.TenantConfig = s.tenantConfig
	s.result.EnvConfig = s.envConfig
	s.runner.Context.Trace("Configuration initialized OK")
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

	projectRegistry, err := s.projectContainerRegistry(projectRoot, envName)
	if err != nil || projectRegistry != "" {
		return projectRegistry, err
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
	return s.promptContainerRegistry(tenant, envName)
}

func (s bootstrapRunner) projectContainerRegistry(projectRoot, envName string) (string, error) {
	if projectRoot == "" || s.LoadProjectConfig == nil {
		return "", nil
	}
	projectConfig, _, err := s.LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			return "", nil
		}
		return "", err
	}
	return projectConfig.ContainerRegistryForEnvironment(envName), nil
}

func (s bootstrapRunner) promptContainerRegistry(tenant, envName string) (string, error) {
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

	projectConfig, err := s.loadProjectConfigForContainerRegistry(projectRoot)
	if err != nil {
		return err
	}

	if projectConfig.ContainerRegistryForEnvironment(envName) == registry {
		return nil
	}

	projectConfig.SetContainerRegistryForEnvironment(envName, registry)
	return s.SaveProjectConfig(projectRoot, projectConfig)
}

func (s bootstrapRunner) loadProjectConfigForContainerRegistry(projectRoot string) (ProjectConfig, error) {
	if s.LoadProjectConfig == nil {
		return ProjectConfig{}, nil
	}
	projectConfig, _, err := s.LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			return ProjectConfig{}, nil
		}
		return ProjectConfig{}, err
	}
	return projectConfig, nil
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
