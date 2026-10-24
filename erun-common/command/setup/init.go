package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/infra/remoteinit"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

var (
	ErrTenantInitializationCancelled      = errors.New("tenant initialization cancelled by user")
	ErrEnvironmentInitializationCancelled = errors.New("environment initialization cancelled by user")
	ErrKubernetesContextCancelled         = errors.New("kubernetes context association cancelled by user")
	ErrContainerRegistryCancelled         = errors.New("container registry configuration cancelled by user")
	ErrTenantSelectionCancelled           = errors.New("tenant selection cancelled by user")
)

type BootstrapStore interface {
	LoadERunConfig() (configpkg.ERunConfig, string, error)
	SaveERunConfig(configpkg.ERunConfig) error
	ListTenantConfigs() ([]configpkg.TenantConfig, error)
	LoadTenantConfig(string) (configpkg.TenantConfig, string, error)
	SaveTenantConfig(configpkg.TenantConfig) error
	LoadEnvConfig(string, string) (configpkg.EnvConfig, string, error)
	SaveEnvConfig(string, configpkg.EnvConfig) error
}

type (
	WorkDirFunc            func() (string, error)
	SelectTenantFunc       func([]configpkg.TenantConfig) (TenantSelectionResult, error)
	PromptValueFunc        func(string) (string, error)
	ProjectConfigSaverFunc func(string, configpkg.ProjectConfig) error
	RemoteRuntimeWaitFunc  func(configpkg.ShellLaunchParams) error
)

type (
	ConfirmFunc func(label string) (bool, error)
)

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
	ERunConfig          configpkg.ERunConfig
	TenantConfig        configpkg.TenantConfig
	EnvConfig           configpkg.EnvConfig
	CreatedERunConfig   bool
	CreatedTenantConfig bool
	CreatedEnvConfig    bool
}

type BootstrapInitDependencies struct {
	Store           BootstrapStore
	FindProjectRoot shared.ProjectFinderFunc
	GetWorkingDir   WorkDirFunc
	SelectTenant    SelectTenantFunc
	// TenantSelectionUnavailable tells the tenant-selection policy that this
	// run's transport has no way to ask anyone which tenant to use -- stdin is
	// not a terminal and carries no answer, an agent is running unattended. One
	// tenant is not a choice, so it is used without asking; a real choice is
	// refused with an error naming the flag that resolves it, instead of
	// printing a menu nobody can answer and failing on EOF.
	TenantSelectionUnavailable bool
	Confirm                    ConfirmFunc
	PromptKubernetesContext    PromptValueFunc
	PromptContainerRegistry    PromptValueFunc
	PromptRemoteRepositoryURL  PromptValueFunc
	PromptCodeCommitSSHKeyID   PromptValueFunc
	EnsureKubernetesNamespace  deploy.NamespaceEnsurerFunc
	LoadProjectConfig          shared.ProjectConfigLoaderFunc
	SaveProjectConfig          ProjectConfigSaverFunc
	WaitForRemoteRuntime       RemoteRuntimeWaitFunc
	RunRemoteCommand           kube.RemoteCommandRunnerFunc
	DeployHelmChart            deploy.HelmChartDeployerFunc
	Sleep                      core.SleepFunc
	// ProbeHostedRegistry answers whether erun's hosted registry can be pushed
	// to. Unset defaults to a real probe, so a caller that wires nothing still
	// refuses an unreachable registry instead of writing config whose pushes
	// fail later, far from the choice that caused it.
	ProbeHostedRegistry HostedRegistryProbeFunc
	Context             core.Context
}

// HostedRegistryProbeFunc reports the hosted registry's current availability.
type HostedRegistryProbeFunc func(context.Context) HostedRegistryStatus

type BootstrapRunner struct {
	BootstrapInitDependencies
	Context core.Context
}

type bootstrapRunState struct {
	runner                BootstrapRunner
	params                open.BootstrapInitParams
	result                BootstrapInitResult
	detected              projectContext
	tenants               []configpkg.TenantConfig
	tenantsLoaded         bool
	setDefaultTenant      bool
	defaultTenantResolved bool
	toolConfig            configpkg.ERunConfig
	toolConfigMissing     bool
	tenant                string
	tenantConfig          configpkg.TenantConfig
	tenantConfigChanged   bool
	envName               string
	envConfig             configpkg.EnvConfig
	envConfigChanged      bool
}

func RunBootstrapInitWithDependencies(deps BootstrapInitDependencies, params open.BootstrapInitParams) (BootstrapInitResult, error) {
	return BootstrapRunner{
		BootstrapInitDependencies: deps,
		Context:                   deps.Context,
	}.run(params)
}

func (s BootstrapRunner) run(params open.BootstrapInitParams) (BootstrapInitResult, error) {
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
	if err := configpkg.ValidateRuntimePodResources(s.params.RuntimePod); err != nil {
		return err
	}
	if err := configpkg.ValidateRuntimeDindPodResources(s.params.RuntimeDindPod); err != nil {
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
	if errors.Is(err, configpkg.ErrNotInGitRepository) {
		s.runner.Context.Logger.Error("erun config is not initialized. Run erun in project directory.")
	}
	return projectContext{}, err
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
	if s.envConfig.ResolvedType() == configpkg.EnvironmentTypeHost {
		s.runner.Context.Trace("init: host environment has no pod; no devops scaffold is written")
		return nil
	}
	projectRoot := s.projectRoot()
	if s.params.RemoteWorktree() {
		return s.ensureRemoteDevopsAssets(projectRoot)
	}
	s.runner.Context.Trace("init: runtime deploys use the published " + configpkg.DevopsComponentName + " chart; no devops scaffold is written")
	return nil
}

func (s *bootstrapRunState) ensureRemoteDevopsAssets(projectRoot string) error {
	req, repository, provisioned, err := s.runner.ensureRemoteRepository(s.params, s.tenant, s.envName, projectRoot, s.envConfig)
	if err != nil {
		return err
	}
	// Persist what this run resolved so a later `erun deploy` carries the same
	// secret name forward instead of losing it on the next helm upgrade.
	if provisioned.registryCredential != s.envConfig.RegistryCredentialSecretName {
		s.envConfig.RegistryCredentialSecretName = provisioned.registryCredential
		s.envConfigChanged = true
	}
	if provisioned.platformAlias != s.envConfig.PlatformAliasSecretName {
		s.envConfig.PlatformAliasSecretName = provisioned.platformAlias
		s.envConfigChanged = true
	}
	if s.params.Bootstrap {
		s.runner.Context.Info("init: --bootstrap is deprecated and ignored; remote runtimes deploy the published " + configpkg.DevopsComponentName + " chart")
	}
	return s.runner.writeRemoteInitMarker(req, remoteinit.RemoteInitMarker{
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

func remoteKeyImportLabel(tenant, envName string) string {
	return fmt.Sprintf("Import the SSH public key above for environment %q in tenant %q and continue", envName, tenant)
}

func remoteHostConfigLabel(tenant, envName string) string {
	return fmt.Sprintf("Use existing SSH host config for environment %q in tenant %q", envName, tenant)
}

func normalizeBootstrapParams(params open.BootstrapInitParams) open.BootstrapInitParams {
	params.Tenant = strings.TrimSpace(params.Tenant)
	params.SelectedTenant = strings.TrimSpace(params.SelectedTenant)
	params.ProjectRoot = strings.TrimSpace(params.ProjectRoot)
	params.Environment = strings.TrimSpace(params.Environment)
	params.RuntimeVersion = strings.TrimSpace(params.RuntimeVersion)
	params.RuntimeVersionDefault = strings.TrimSpace(params.RuntimeVersionDefault)
	params.RuntimeImage = strings.TrimSpace(params.RuntimeImage)
	params.RuntimeRegistry = strings.TrimSpace(params.RuntimeRegistry)
	// Trimmed, not normalized: a defaulted limit here would be indistinguishable
	// from an operator asking for that limit, and the reconcile needs to tell an
	// omitted flag from a supplied one.
	params.RuntimePod = configpkg.RuntimePodResources{CPU: strings.TrimSpace(params.RuntimePod.CPU), Memory: strings.TrimSpace(params.RuntimePod.Memory)}
	params.RuntimeDindPod = configpkg.RuntimePodResources{CPU: strings.TrimSpace(params.RuntimeDindPod.CPU), Memory: strings.TrimSpace(params.RuntimeDindPod.Memory)}
	params.KubernetesContext = strings.TrimSpace(params.KubernetesContext)
	params.ContainerRegistry = strings.TrimSpace(params.ContainerRegistry)
	params.RemoteRepositoryURL = strings.TrimSpace(params.RemoteRepositoryURL)
	params.CodeCommitSSHKeyID = strings.TrimSpace(params.CodeCommitSSHKeyID)
	return params
}

func (s BootstrapRunner) withDefaults() BootstrapRunner {
	s = s.withStoreDefaults()
	s = s.withRuntimeDefaults()
	return s.withLoggerDefaults()
}

func (s BootstrapRunner) withStoreDefaults() BootstrapRunner {
	if s.Store == nil {
		s.Store = configpkg.ConfigStore{}
	}
	if s.FindProjectRoot == nil {
		s.FindProjectRoot = configpkg.FindProjectRoot
	}
	if s.GetWorkingDir == nil {
		s.GetWorkingDir = os.Getwd
	}
	if s.LoadProjectConfig == nil {
		s.LoadProjectConfig = configpkg.LoadProjectConfig
	}
	if s.SaveProjectConfig == nil {
		s.SaveProjectConfig = configpkg.SaveProjectConfig
	}
	return s
}

func (s BootstrapRunner) withRuntimeDefaults() BootstrapRunner {
	if s.WaitForRemoteRuntime == nil {
		s.WaitForRemoteRuntime = open.WaitForShellDeployment
	}
	if s.RunRemoteCommand == nil {
		s.RunRemoteCommand = kube.RunRemoteCommand
	}
	if s.DeployHelmChart == nil {
		s.DeployHelmChart = deploy.DeployHelmChart
	}
	if s.Sleep == nil {
		s.Sleep = time.Sleep
	}
	return s
}

func (s BootstrapRunner) withLoggerDefaults() BootstrapRunner {
	if s.Context.Logger.IsUnconfigured() {
		s.Context.Logger = core.NewLoggerWithWriters(-1, io.Discard, io.Discard)
	}
	return s
}

func (s BootstrapRunner) confirmTenant(params open.BootstrapInitParams, tenant, projectRoot string) (bool, error) {
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

func (s BootstrapRunner) confirmEnvironment(params open.BootstrapInitParams, tenant, envName string) error {
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

func (s BootstrapRunner) confirm(interaction BootstrapInitInteraction) (bool, error) {
	if s.Confirm == nil {
		return false, BootstrapInitInteractionError{Interaction: interaction}
	}
	confirmed, err := s.Confirm(interaction.Label)
	if err != nil {
		return false, err
	}
	return confirmed, nil
}

func saveEnvConfig(store BootstrapStore, tenant string, config configpkg.EnvConfig) error {
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
