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
	DefaultContainerRegistry = "ghcr.io/sophium"
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
	RuntimePod               RuntimePodResources
	NoGit                    bool
	KubernetesContext        string
	ContainerRegistry        string
	// ClusterRegistry, when set, seeds the new env with a resolvable cluster:
	// registry entry (addresses resolved from the env's kube-context) instead of
	// the static ContainerRegistry string. Set by `erun init --cluster-registry`.
	ClusterRegistry *ClusterRegistry
	// MCPAuthPublicKeyPath, when set, points at a PEM public key the init-time
	// runtime deploy trusts so the env's erun-mcp edge requires a bearer signed by
	// it — mirrors `erun deploy --mcp-auth-public-key`. The desktop sets it so
	// init's single deploy already carries MCP auth and no post-init redeploy
	// (which would roll the just-created pod) is needed.
	MCPAuthPublicKeyPath string
	// Type is the canonical environment type and takes precedence over the
	// legacy Remote bool; when unset the type is derived from Remote for
	// backward compatibility with --remote flag callers.
	Type                    EnvironmentType
	Remote                  bool
	RemoteRepositoryURL     string
	CodeCommitSSHKeyID      string
	Bootstrap               bool
	ConfirmTenant           *bool
	ConfirmEnvironment      *bool
	ConfirmRemoteHostConfig *bool
	ConfirmRemoteKeyImport  *bool
	AutoApprove             bool
	ResolveTenant           bool
	DisableBuildScript      bool
	PlatformAccount         bool
}

// ResolvedType returns the new env's type: an explicit Type wins, otherwise it
// is derived from Remote to preserve the pre-type behavior of `erun init
// [--remote]`.
func (p BootstrapInitParams) ResolvedType() EnvironmentType {
	if p.Type.IsValid() {
		return p.Type
	}
	if p.Remote {
		return EnvironmentTypeRemoteAgent
	}
	return EnvironmentTypeLocalAgent
}

// RemoteWorktree reports whether the new env's worktree will live outside the
// local machine.
func (p BootstrapInitParams) RemoteWorktree() bool {
	return p.ResolvedType() != EnvironmentTypeLocalAgent
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
	if err := ValidateRuntimePodResources(s.params.RuntimePod); err != nil {
		return err
	}
	if err := s.validateRemoteParams(); err != nil {
		return err
	}
	if err := s.loadBootstrapConfigs(); err != nil {
		return err
	}
	s.emitInitializingTrace()
	if err := s.applyBootstrapConfigChanges(); err != nil {
		s.emitInitializationFailedTrace()
		return err
	}
	s.finish()
	return nil
}

// emitInitializingTrace records the in-flight init activity: the desktop's
// activity-queue trace handler parses this `==> Initializing` line (mirrors
// RunHelmDeploy's `==> Deploying`). No-op before tenant/env resolve, when there
// is nothing to register.
func (s *bootstrapRunState) emitInitializingTrace() {
	if s.tenant == "" || s.envName == "" {
		return
	}
	s.runner.Context.Info("==> Initializing " + s.tenant + "/" + s.envName)
}

// emitInitializationFailedTrace finalizes the umbrella activity entry when init
// fails after the Initializing line was emitted; no-op otherwise.
func (s *bootstrapRunState) emitInitializationFailedTrace() {
	if s.tenant == "" || s.envName == "" {
		return
	}
	s.runner.Context.Info("==> Initialization failed " + s.tenant + "/" + s.envName)
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
	if !s.params.RemoteWorktree() {
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
	if s.params.RemoteWorktree() {
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
	if s.tenant == "" && !s.params.RemoteWorktree() {
		s.tenant = s.toolConfig.DefaultTenant
	}
	if err := s.resolveTenantFromSelection(); err != nil {
		return err
	}
	if s.tenant == "" && !s.params.RemoteWorktree() {
		project, err := s.detectProject()
		if err != nil {
			return err
		}
		s.tenant = project.tenant
	}
	if s.params.RemoteWorktree() {
		s.params.ProjectRoot = RemoteWorktreePathForRepoName(s.tenant)
	}
	return nil
}

func (s *bootstrapRunState) resolveTenantFromProject() error {
	if s.tenant != "" || s.params.RemoteWorktree() {
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
	if currentTenant, found := findTenantForDirectory(workingDir, tenants, s.envsByTenant(tenants)); found {
		s.tenant = currentTenant.Name
	}
	return nil
}

// envsByTenant loads each tenant's environments for cwd→tenant matching. It is
// best-effort: a store that does not expose ListEnvConfigs (or a tenant whose
// envs fail to load) yields no envs for that tenant, so matching simply finds
// no owner and the caller falls back to the default-tenant / selection path.
func (s *bootstrapRunState) envsByTenant(tenants []TenantConfig) map[string][]EnvConfig {
	lister, ok := s.runner.Store.(interface {
		ListEnvConfigs(string) ([]EnvConfig, error)
	})
	if !ok {
		return nil
	}
	envsByTenant := make(map[string][]EnvConfig, len(tenants))
	for _, tenant := range tenants {
		if envs, err := lister.ListEnvConfigs(tenant.Name); err == nil {
			envsByTenant[tenant.Name] = envs
		}
	}
	return envsByTenant
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
	if err := ValidateTenantName(s.tenant); err != nil {
		return err
	}
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
	if projectRoot != "" || s.params.RemoteWorktree() {
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
	if err := s.runner.saveProjectContainerRegistry(envProjectRoot, s.envName, containerRegistry, s.params); err != nil {
		return err
	}
	s.runner.Context.Trace("Adding new environment")
	// Every env type records localRepoPath — the single source for cwd→tenant
	// matching, the open repo path, and the deploy worktree repo name. For
	// local-agent envs it is also the hostPath mounted into the pod; remote/runtime
	// envs use a PVC worktree, so the value is the init-time project root (its
	// basename names the in-pod worktree) but is never mounted.
	s.envConfig = EnvConfig{
		Name:               s.envName,
		Type:               s.params.ResolvedType(),
		LocalRepoPath:      envProjectRoot,
		KubernetesContext:  kubernetesContext,
		CloudProviderAlias: cloudProviderAlias,
		ManagedCloud:       managedCloud,
		RuntimeVersion:     strings.TrimSpace(s.params.RuntimeVersion),
		RuntimeImage:       strings.TrimSpace(s.params.RuntimeImage),
		RuntimePod:         NormalizeRuntimePodResources(s.params.RuntimePod),
		DisableBuildScript: s.params.DisableBuildScript,
		PlatformAccount:    s.params.PlatformAccount,
		// Seed the registries into the FIRST persisted config. The init-time deploy
		// (ensureDevopsAssets) reads the env config from disk before
		// saveEnvConfigIfChanged runs, so registries set only in-memory here were
		// ignored — the initial deploy fell back to the default registry even with
		// --container-registry or --cluster-registry. Persisting them up front fixes
		// that so the new env deploys to the configured registry from the start.
		ContainerRegistries: seedInitContainerRegistries(s.params, containerRegistry),
	}
	if err := saveEnvConfig(s.runner.Store, s.tenant, s.envConfig); err != nil {
		return err
	}
	s.result.CreatedEnvConfig = true
	return nil
}

// seedInitContainerRegistries returns the marked list a new env is created with:
// a resolvable cluster: entry when the operator requested the in-cluster registry
// (--cluster-registry), otherwise the single static registry resolved from
// --container-registry / the prompt.
func seedInitContainerRegistries(params BootstrapInitParams, registry string) ContainerRegistries {
	if params.ClusterRegistry != nil {
		return ClusterContainerRegistries(*params.ClusterRegistry)
	}
	return SingleContainerRegistries(registry)
}

func (s *bootstrapRunState) envProjectRoot() (string, error) {
	envProjectRoot := s.params.ProjectRoot
	if envProjectRoot != "" || s.params.RemoteWorktree() {
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
		Type:               s.params.ResolvedType(),
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
	if !s.params.RemoteWorktree() {
		return
	}
	// A remote env persists no host repo path: LocalRepoPath is laptop-only and
	// the worktree lives in-pod, so the project root is resolved from
	// params/tenant rather than threaded through env config here.
	if runtimeVersion := strings.TrimSpace(s.params.RuntimeVersion); runtimeVersion != "" && s.envConfig.RuntimeVersion != runtimeVersion {
		s.envConfig.RuntimeVersion = runtimeVersion
		s.envConfigChanged = true
	}
	if runtimeImage := strings.TrimSpace(s.params.RuntimeImage); runtimeImage != "" && s.envConfig.RuntimeImage != runtimeImage {
		s.envConfig.RuntimeImage = runtimeImage
		s.envConfigChanged = true
	}
	if runtimePod := NormalizeRuntimePodResources(s.params.RuntimePod); runtimePod != NormalizeRuntimePodResources(s.envConfig.RuntimePod) {
		s.envConfig.RuntimePod = runtimePod
		s.envConfigChanged = true
	}
	if !s.envConfig.RemoteWorktree() {
		s.envConfig.Type = s.params.ResolvedType()
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
	if s.params.ClusterRegistry != nil {
		if err := s.runner.saveProjectContainerRegistry(projectRoot, s.envName, "", s.params); err != nil {
			return err
		}
		desired := ClusterContainerRegistries(*s.params.ClusterRegistry)
		if !s.envConfig.ContainerRegistries.Equal(desired) {
			s.envConfig.ContainerRegistries = desired
			s.envConfigChanged = true
		}
		return nil
	}
	current, _ := s.envConfig.ContainerRegistries.BuildRegistry()
	containerRegistry, err := s.runner.resolveContainerRegistry(s.params, s.tenant, s.envName, projectRoot, current, false)
	if err != nil {
		return err
	}
	if containerRegistry == "" {
		return nil
	}
	if err := s.runner.saveProjectContainerRegistry(projectRoot, s.envName, containerRegistry, s.params); err != nil {
		return err
	}
	if existing, _ := s.envConfig.ContainerRegistries.BuildRegistry(); containerRegistry != existing {
		s.envConfig.ContainerRegistries = SingleContainerRegistries(containerRegistry)
		s.envConfigChanged = true
	}
	return nil
}

func (s *bootstrapRunState) projectRoot() string {
	return strings.TrimSpace(s.params.ProjectRoot)
}

// ensureDevopsAssets no longer scaffolds a per-tenant devops module: environments
// deploy the published erun-devops chart, and custom toolchains are a user-authored
// Dockerfile FROM the published runtime image (see the erun-build-env skill).
// Tenants with an existing scaffolded module keep working — deploy prefers a
// local chart when one exists.
func (s *bootstrapRunState) ensureDevopsAssets() error {
	projectRoot := s.projectRoot()
	if s.params.RemoteWorktree() {
		return s.ensureRemoteDevopsAssets(projectRoot)
	}
	s.runner.Context.Trace("init: runtime deploys use the published " + DevopsComponentName + " chart; no devops scaffold is written")
	return nil
}

func (s *bootstrapRunState) ensureRemoteDevopsAssets(projectRoot string) error {
	req, repository, err := s.runner.ensureRemoteRepository(s.params, s.tenant, s.envName, s.envConfig.KubernetesContext, projectRoot, s.envConfig.ContainerRegistries)
	if err != nil {
		return err
	}
	if s.params.Bootstrap {
		s.runner.Context.Info("init: --bootstrap is deprecated and ignored; remote runtimes deploy the published " + DevopsComponentName + " chart")
	}
	return s.runner.writeRemoteInitMarker(req, RemoteInitMarker{
		Tenant:             s.tenant,
		Environment:        s.envName,
		ProjectRoot:        projectRoot,
		RepositoryURL:      repository.URL,
		CodeCommitHost:     repository.CodeCommitHost,
		CodeCommitSSHKeyID: repository.CodeCommitSSHKeyID,
		NoGit:              s.params.NoGit,
		BootstrapComplete:  true,
	})
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
	if s.tenant != "" && s.envName != "" {
		s.runner.Context.Info("==> Initialized " + s.tenant + "/" + s.envName)
	}
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

// maxTenantNameLength caps a tenant name at the RFC 1123 DNS label limit. The
// combined `<tenant>-<env>` namespace is normalized and truncated separately by
// normalizeNamespaceName; this bound just keeps a tenant name a single label.
const maxTenantNameLength = 63

// ValidateTenantName enforces that a tenant name is a single DNS-safe label of
// lowercase letters and digits with no hyphens. Tenant names feed the
// `<tenant>-<env>` namespace mapping (KubernetesNamespaceName); forbidding
// hyphens keeps that mapping unambiguous (split on the first hyphen) and
// injective, so `a-b`+`c` and `a`+`b-c` can no longer collide on one namespace.
//
// The rule is checked before normalization so a caller cannot rely on
// normalizeNamespaceName to silently repair an invalid name. It is enforced at
// tenant creation only; tenants created before this rule keep working unchanged
// (no re-validation on load), preserving back-compat.
func ValidateTenantName(name string) error {
	if name == "" {
		return fmt.Errorf("tenant name is required")
	}
	if len(name) > maxTenantNameLength {
		return fmt.Errorf("invalid tenant name %q: must be at most %d characters", name, maxTenantNameLength)
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		return fmt.Errorf("invalid tenant name %q: tenant names must use only lowercase letters and digits with no hyphens, so the <tenant>-<env> namespace is unambiguous", name)
	}
	return nil
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
	params.RuntimePod = NormalizeRuntimePodResources(params.RuntimePod)
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
	// A cluster registry is written as a resolvable cluster: entry, not a static
	// string, so short-circuit the string resolution/prompt entirely.
	if params.ClusterRegistry != nil {
		return "", nil
	}
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
	registry, _ := projectConfig.ContainerRegistriesForEnvironment(envName).BuildRegistry()
	return registry, nil
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

func (s bootstrapRunner) saveProjectContainerRegistry(projectRoot, envName, registry string, params BootstrapInitParams) error {
	// Remote envs record the registry on the env config only; the shared project
	// config is for local-agent envs whose worktree lives in this repo.
	if params.Remote {
		return nil
	}
	if projectRoot == "" || envName == "" || s.SaveProjectConfig == nil {
		return nil
	}

	if params.ClusterRegistry != nil {
		return s.saveClusterContainerRegistry(projectRoot, envName, *params.ClusterRegistry)
	}
	return s.savePlainContainerRegistry(projectRoot, envName, registry)
}

func (s bootstrapRunner) saveClusterContainerRegistry(projectRoot, envName string, cluster ClusterRegistry) error {
	projectConfig, err := s.loadProjectConfigForContainerRegistry(projectRoot)
	if err != nil {
		return err
	}
	desired := ClusterContainerRegistries(cluster)
	if projectConfig.ContainerRegistriesForEnvironment(envName).Equal(desired) {
		return nil
	}
	projectConfig.SetContainerRegistriesForEnvironment(envName, desired)
	return s.SaveProjectConfig(projectRoot, projectConfig)
}

func (s bootstrapRunner) savePlainContainerRegistry(projectRoot, envName, registry string) error {
	if registry == "" {
		return nil
	}
	projectConfig, err := s.loadProjectConfigForContainerRegistry(projectRoot)
	if err != nil {
		return err
	}
	if existing, _ := projectConfig.ContainerRegistriesForEnvironment(envName).BuildRegistry(); existing == registry {
		return nil
	}
	projectConfig.SetContainerRegistriesForEnvironment(envName, SingleContainerRegistries(registry))
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

// findTenantForDirectory resolves which tenant owns the working directory,
// longest matching repo path wins. The host path lives on the env, not the
// tenant, because a tenant can host both local and remote envs; a remote env
// with no host worktree owns no directory. Ties across tenants resolve to no
// match, so the caller falls back to the default-tenant / selection path rather
// than guessing.
func findTenantForDirectory(dir string, tenants []TenantConfig, envsByTenant map[string][]EnvConfig) (TenantConfig, bool) {
	cleanDir := filepath.Clean(dir)
	bestTenant := -1
	bestLen := -1
	ambiguous := false

	for i, tenant := range tenants {
		for _, env := range envsByTenant[tenant.Name] {
			repoPath := strings.TrimSpace(env.EffectiveLocalRepoPath())
			if repoPath == "" {
				continue
			}
			if !isWithinDirectory(cleanDir, filepath.Clean(repoPath)) {
				continue
			}
			switch {
			case len(repoPath) > bestLen:
				bestTenant, bestLen, ambiguous = i, len(repoPath), false
			case len(repoPath) == bestLen && i != bestTenant:
				ambiguous = true
			}
		}
	}

	if bestTenant == -1 || ambiguous {
		return TenantConfig{}, false
	}
	return tenants[bestTenant], true
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
	if !stored.RemoteWorktree() {
		stored.ContainerRegistries = nil
	}
	return store.SaveEnvConfig(tenant, stored)
}

type projectContext struct {
	tenant string
	root   string
	loaded bool
}
