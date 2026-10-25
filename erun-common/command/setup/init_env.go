package setup

import (
	"errors"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/seam/bootstrap"
	"github.com/sophium/erun/erun-common/infra/activity/managedcloud"
	componentspkg "github.com/sophium/erun/erun-common/primitive/deploy/components"
	"github.com/sophium/erun/erun-common/primitive/deploy/runtimeimage"
)

func defaultBootstrapEnvironment(envName string) string {
	if envName != "" {
		return envName
	}
	return configpkg.DefaultEnvironment
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
		s.envName = configpkg.DefaultEnvironment
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
	case errors.Is(err, configpkg.ErrNotInitialized):
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
	if s.params.ResolvedType() == configpkg.EnvironmentTypeHost {
		return s.createHostEnvConfig(envProjectRoot)
	}
	kubernetesContext, cloudProviderAlias, managedCloud, err := s.resolveNewEnvCloudConfig()
	if err != nil {
		return err
	}
	containerRegistry, err := s.runner.ResolveContainerRegistry(s.params, s.tenant, s.envName, envProjectRoot, "", true)
	if err != nil {
		return err
	}
	if err := s.runner.saveProjectContainerRegistry(envProjectRoot, s.envName, containerRegistry, s.params); err != nil {
		return err
	}
	s.runner.Context.Trace("Adding new environment")
	// localRepoPath drives cwd->tenant matching and names the in-pod worktree; only
	// local-agent envs mount it.
	s.envConfig = configpkg.EnvConfig{
		Name:               s.envName,
		Type:               s.params.ResolvedType(),
		LocalRepoPath:      envProjectRoot,
		KubernetesContext:  kubernetesContext,
		CloudProviderAlias: cloudProviderAlias,
		ManagedCloud:       managedCloud,
		RuntimeVersion:     s.params.ResolvedRuntimeVersion(),
		RuntimeImage:       runtimeimage.StripRuntimeImageTag(s.params.RuntimeImage),
		RuntimeRegistry:    strings.TrimSpace(s.params.RuntimeRegistry),
		// Without it, a redeploy drops the MCP edge to unauthenticated.
		MCPAuthPublicKeyPath: strings.TrimSpace(s.params.MCPAuthPublicKeyPath),
		// A private runtime image cannot start without these.
		ImagePullSecrets:   configpkg.NormalizeImagePullSecrets(s.params.ImagePullSecrets),
		RuntimePod:         configpkg.NormalizeRuntimePodResources(s.params.RuntimePod),
		RuntimeDindPod:     configpkg.NormalizeRuntimeDindPodResources(s.params.RuntimeDindPod),
		DisableBuildScript: s.params.DisableBuildScript,
		PlatformAccount:    s.params.PlatformAccount,
		Deploy:             initDeployConfig(s.params),
		// The init-time deploy reads the env config from disk before it is saved, so
		// registries must be in the first persisted config.
		ContainerRegistries: seedInitContainerRegistries(s.params, containerRegistry),
	}
	if err := saveEnvConfig(s.runner.Store, s.tenant, s.envConfig); err != nil {
		return err
	}
	s.result.CreatedEnvConfig = true
	return nil
}

// createHostEnvConfig records only name, type and directory: a host env must
// never contact a cluster.
func (s *bootstrapRunState) createHostEnvConfig(envProjectRoot string) error {
	if err := configpkg.ValidateHostRepoPath(configpkg.EnvironmentTypeHost, envProjectRoot); err != nil {
		return fmt.Errorf("cannot create %s/%s as type %s: %w", s.tenant, s.envName, configpkg.EnvironmentTypeHost, err)
	}
	envProjectRoot = strings.TrimSpace(envProjectRoot)
	if err := s.runner.confirmEnvironment(s.params, s.tenant, s.envName); err != nil {
		return err
	}
	s.runner.Context.Trace("Adding new environment")
	s.envConfig = configpkg.EnvConfig{
		Name:          s.envName,
		Type:          configpkg.EnvironmentTypeHost,
		LocalRepoPath: envProjectRoot,
	}
	if err := saveEnvConfig(s.runner.Store, s.tenant, s.envConfig); err != nil {
		return err
	}
	s.result.CreatedEnvConfig = true
	return nil
}

func initDeployConfig(params bootstrap.BootstrapInitParams) configpkg.EnvironmentDeployConfig {
	components, given := initComponentsOverride(params)
	if !given {
		return configpkg.EnvironmentDeployConfig{}
	}
	return configpkg.EnvironmentDeployConfig{Components: components}
}

// initComponentsOverride distinguishes an explicitly empty --components, which
// clears a saved selection, from an omitted flag.
func initComponentsOverride(params bootstrap.BootstrapInitParams) ([]string, bool) {
	if params.Components == nil {
		return nil, false
	}
	return componentspkg.NormalizeComponentNames(*params.Components), true
}

func describeComponentsSetting(components []string) string {
	if len(components) == 0 {
		return "(cleared — deploy now follows the repo k8s.deployments plan)"
	}
	return strings.Join(components, ",")
}

func seedInitContainerRegistries(params bootstrap.BootstrapInitParams, registry string) configpkg.ContainerRegistries {
	if params.ClusterRegistry != nil {
		return configpkg.ClusterContainerRegistries(*params.ClusterRegistry)
	}
	return configpkg.SingleContainerRegistries(registry)
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
	if errors.Is(err, configpkg.ErrNotInGitRepository) {
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
	managedCloud, err := managedcloud.ManagedCloudEnvironment(s.runner.Store, configpkg.EnvConfig{
		KubernetesContext:  kubernetesContext,
		CloudProviderAlias: cloudProviderAlias,
		Type:               s.params.ResolvedType(),
	})
	return kubernetesContext, cloudProviderAlias, managedCloud, err
}

func (s *bootstrapRunState) updateEnvConfig() error {
	if err := s.updateExistingEnvSettings(); err != nil {
		return err
	}
	// A host env has no cluster to reconcile against.
	if s.envConfig.ResolvedType() == configpkg.EnvironmentTypeHost {
		return nil
	}
	kubernetesContext, err := s.updateEnvKubernetesContext()
	if err != nil {
		return err
	}
	if err := s.updateEnvCloudProvider(kubernetesContext); err != nil {
		return err
	}
	return s.updateEnvContainerRegistry()
}
