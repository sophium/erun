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
	configprojectroot "github.com/sophium/erun/erun-common/base/config/projectroot"
	"github.com/sophium/erun/erun-common/base/config/runtimeresources"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/bootstrap"
	"github.com/sophium/erun/erun-common/base/seam/envstore"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
	"github.com/sophium/erun/erun-common/base/seam/kubeapi"
	seamprojectroot "github.com/sophium/erun/erun-common/base/seam/projectroot"
	"github.com/sophium/erun/erun-common/base/seam/runtimepod"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/deploy/helmrun"
	"github.com/sophium/erun/erun-common/primitive/open/podwait"
)

var (
	ErrTenantInitializationCancelled      = errors.New("tenant initialization cancelled by user")
	ErrEnvironmentInitializationCancelled = errors.New("environment initialization cancelled by user")
	ErrKubernetesContextCancelled         = errors.New("kubernetes context association cancelled by user")
	ErrContainerRegistryCancelled         = errors.New("container registry configuration cancelled by user")
	ErrTenantSelectionCancelled           = errors.New("tenant selection cancelled by user")
)

type (
	WorkDirFunc            func() (string, error)
	SelectTenantFunc       func([]model.TenantConfig) (model.TenantSelectionResult, error)
	PromptValueFunc        func(string) (string, error)
	ProjectConfigSaverFunc func(string, configpkg.ProjectConfig) error
)

type (
	ConfirmFunc func(label string) (bool, error)
)

type BootstrapInitInteractionError struct {
	Interaction model.BootstrapInitInteraction
}

func (e BootstrapInitInteractionError) Error() string {
	return "bootstrap init interaction required: " + string(e.Interaction.Type)
}

func AsBootstrapInitInteraction(err error) (model.BootstrapInitInteraction, bool) {
	var interactionErr BootstrapInitInteractionError
	if !errors.As(err, &interactionErr) {
		return model.BootstrapInitInteraction{}, false
	}
	return interactionErr.Interaction, true
}

type BootstrapInitResult struct {
	ERunConfig          configpkg.ERunConfig
	TenantConfig        model.TenantConfig
	EnvConfig           configpkg.EnvConfig
	CreatedERunConfig   bool
	CreatedTenantConfig bool
	CreatedEnvConfig    bool
}

type BootstrapInitDependencies struct {
	Store           envstore.BootstrapStore
	FindProjectRoot seamprojectroot.ProjectFinderFunc
	GetWorkingDir   WorkDirFunc
	SelectTenant    SelectTenantFunc
	// TenantSelectionUnavailable means nobody can be asked: a single tenant is
	// used, and a real choice is refused naming the flag that resolves it.
	TenantSelectionUnavailable bool
	Confirm                    ConfirmFunc
	PromptKubernetesContext    PromptValueFunc
	PromptContainerRegistry    PromptValueFunc
	PromptRemoteRepositoryURL  PromptValueFunc
	PromptCodeCommitSSHKeyID   PromptValueFunc
	EnsureKubernetesNamespace  kubeapi.NamespaceEnsurerFunc
	LoadProjectConfig          seamprojectroot.ProjectConfigLoaderFunc
	SaveProjectConfig          ProjectConfigSaverFunc
	WaitForRemoteRuntime       runtimepod.RemoteRuntimeWaitFunc
	RunRemoteCommand           runtimepod.RemoteCommandRunnerFunc
	DeployHelmChart            helmdeploy.HelmChartDeployerFunc
	Sleep                      core.SleepFunc
	// ProbeHostedRegistry defaults to a real probe when unset.
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
	params                bootstrap.BootstrapInitParams
	result                BootstrapInitResult
	detected              projectContext
	tenants               []model.TenantConfig
	tenantsLoaded         bool
	setDefaultTenant      bool
	defaultTenantResolved bool
	toolConfig            configpkg.ERunConfig
	toolConfigMissing     bool
	tenant                string
	tenantConfig          model.TenantConfig
	tenantConfigChanged   bool
	envName               string
	envConfig             configpkg.EnvConfig
	envConfigChanged      bool
}

func RunBootstrapInitWithDependencies(deps BootstrapInitDependencies, params bootstrap.BootstrapInitParams) (BootstrapInitResult, error) {
	return BootstrapRunner{
		BootstrapInitDependencies: deps,
		Context:                   deps.Context,
	}.run(params)
}

func (s BootstrapRunner) run(params bootstrap.BootstrapInitParams) (BootstrapInitResult, error) {
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
	if err := runtimeresources.ValidateRuntimePodResources(s.params.RuntimePod); err != nil {
		return err
	}
	if err := runtimeresources.ValidateRuntimeDindPodResources(s.params.RuntimeDindPod); err != nil {
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

// emitInitializingTrace's `==> Initializing` line is parsed by the desktop's
// activity-queue trace handler.
func (s *bootstrapRunState) emitInitializingTrace() {
	if s.tenant == "" || s.envName == "" {
		return
	}
	s.runner.Context.Info("==> Initializing " + s.tenant + "/" + s.envName)
}

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

// ensureDevopsAssets scaffolds no devops module: envs deploy the published
// erun-devops chart, though deploy still prefers an existing local one.
func (s *bootstrapRunState) ensureDevopsAssets() error {
	if s.envConfig.ResolvedType() == configpkg.EnvironmentTypeHost {
		s.runner.Context.Trace("init: host environment has no pod; no devops scaffold is written")
		return nil
	}
	projectRoot := s.projectRoot()
	if s.params.RemoteWorktree() {
		return s.ensureRemoteDevopsAssets(projectRoot)
	}
	s.runner.Context.Trace("init: runtime deploys use the published " + runtimetarget.DevopsComponentName + " chart; no devops scaffold is written")
	return nil
}

func (s *bootstrapRunState) ensureRemoteDevopsAssets(projectRoot string) error {
	req, repository, provisioned, err := s.runner.ensureRemoteRepository(s.params, s.tenant, s.envName, projectRoot, s.envConfig)
	if err != nil {
		return err
	}
	// Persist so the next helm upgrade keeps the secret name.
	if provisioned.registryCredential != s.envConfig.RegistryCredentialSecretName {
		s.envConfig.RegistryCredentialSecretName = provisioned.registryCredential
		s.envConfigChanged = true
	}
	if provisioned.platformAlias != s.envConfig.PlatformAliasSecretName {
		s.envConfig.PlatformAliasSecretName = provisioned.platformAlias
		s.envConfigChanged = true
	}
	if s.params.Bootstrap {
		s.runner.Context.Info("init: --bootstrap is deprecated and ignored; remote runtimes deploy the published " + runtimetarget.DevopsComponentName + " chart")
	}
	return s.runner.writeRemoteInitMarker(req, model.RemoteInitMarker{
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

func normalizeBootstrapParams(params bootstrap.BootstrapInitParams) bootstrap.BootstrapInitParams {
	params.Tenant = strings.TrimSpace(params.Tenant)
	params.SelectedTenant = strings.TrimSpace(params.SelectedTenant)
	params.ProjectRoot = strings.TrimSpace(params.ProjectRoot)
	params.Environment = strings.TrimSpace(params.Environment)
	params.RuntimeVersion = strings.TrimSpace(params.RuntimeVersion)
	params.RuntimeVersionDefault = strings.TrimSpace(params.RuntimeVersionDefault)
	params.RuntimeImage = strings.TrimSpace(params.RuntimeImage)
	params.RuntimeRegistry = strings.TrimSpace(params.RuntimeRegistry)
	// Trimmed, not normalized: reconcile must tell an omitted flag from a supplied
	// default.
	params.RuntimePod = model.RuntimePodResources{CPU: strings.TrimSpace(params.RuntimePod.CPU), Memory: strings.TrimSpace(params.RuntimePod.Memory)}
	params.RuntimeDindPod = model.RuntimePodResources{CPU: strings.TrimSpace(params.RuntimeDindPod.CPU), Memory: strings.TrimSpace(params.RuntimeDindPod.Memory)}
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
		s.FindProjectRoot = configprojectroot.FindProjectRoot
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
		s.WaitForRemoteRuntime = podwait.WaitForShellDeployment
	}
	if s.RunRemoteCommand == nil {
		s.RunRemoteCommand = kube.RunRemoteCommand
	}
	if s.DeployHelmChart == nil {
		s.DeployHelmChart = helmrun.DeployHelmChart
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

func (s BootstrapRunner) confirmTenant(params bootstrap.BootstrapInitParams, tenant, projectRoot string) (bool, error) {
	if params.AutoApprove {
		return true, nil
	}
	if params.ConfirmTenant != nil {
		return *params.ConfirmTenant, nil
	}
	return s.confirm(model.BootstrapInitInteraction{
		Type:  model.BootstrapInitInteractionConfirmTenant,
		Label: tenantConfirmationLabel(tenant, projectRoot),
	})
}

func (s BootstrapRunner) confirmEnvironment(params bootstrap.BootstrapInitParams, tenant, envName string) error {
	if params.AutoApprove {
		return nil
	}
	if params.ConfirmEnvironment != nil {
		if *params.ConfirmEnvironment {
			return nil
		}
		return ErrEnvironmentInitializationCancelled
	}
	confirmed, err := s.confirm(model.BootstrapInitInteraction{
		Type:  model.BootstrapInitInteractionConfirmEnvironment,
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

func (s BootstrapRunner) confirm(interaction model.BootstrapInitInteraction) (bool, error) {
	if s.Confirm == nil {
		return false, BootstrapInitInteractionError{Interaction: interaction}
	}
	confirmed, err := s.Confirm(interaction.Label)
	if err != nil {
		return false, err
	}
	return confirmed, nil
}

func saveEnvConfig(store envstore.BootstrapStore, tenant string, config configpkg.EnvConfig) error {
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
