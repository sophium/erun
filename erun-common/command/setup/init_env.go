package setup

import (
	"errors"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/open"
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
	// Every env type records localRepoPath — the single source for cwd→tenant
	// matching, the open repo path, and the deploy worktree repo name. For
	// local-agent envs it is also the hostPath mounted into the pod; remote/runtime
	// envs use a PVC worktree, so the value is the init-time project root (its
	// basename names the in-pod worktree) but is never mounted.
	s.envConfig = configpkg.EnvConfig{
		Name:               s.envName,
		Type:               s.params.ResolvedType(),
		LocalRepoPath:      envProjectRoot,
		KubernetesContext:  kubernetesContext,
		CloudProviderAlias: cloudProviderAlias,
		ManagedCloud:       managedCloud,
		RuntimeVersion:     s.params.ResolvedRuntimeVersion(),
		RuntimeImage:       deploy.StripRuntimeImageTag(s.params.RuntimeImage),
		RuntimeRegistry:    strings.TrimSpace(s.params.RuntimeRegistry),
		// Record the key init's runtime deploy trusted, so the next redeploy
		// rethreads it instead of dropping the env's MCP edge to unauthenticated.
		MCPAuthPublicKeyPath: strings.TrimSpace(s.params.MCPAuthPublicKeyPath),
		// The pod cannot start on a private runtime image without these, so a new
		// env records them from the start rather than on a second init.
		ImagePullSecrets:   configpkg.NormalizeImagePullSecrets(s.params.ImagePullSecrets),
		RuntimePod:         configpkg.NormalizeRuntimePodResources(s.params.RuntimePod),
		RuntimeDindPod:     configpkg.NormalizeRuntimeDindPodResources(s.params.RuntimeDindPod),
		DisableBuildScript: s.params.DisableBuildScript,
		PlatformAccount:    s.params.PlatformAccount,
		Deploy:             initDeployConfig(s.params),
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

// createHostEnvConfig creates a host env: a worktree directory on this
// machine with no pod and no cluster at all. None of the runtime/cloud/
// registry fields below apply to it, so it records only what a host env
// actually has — a name, its type, and the directory it names — and never
// resolves a kubernetes context, a cloud provider alias, or a container
// registry. A host environment must not contact a cluster during init any
// more than it does at any other time.
func (s *bootstrapRunState) createHostEnvConfig(envProjectRoot string) error {
	if err := configpkg.ValidateHostRepoPath(configpkg.EnvironmentTypeHost, envProjectRoot); err != nil {
		return fmt.Errorf("cannot create %s/%s as type %s: %w", s.tenant, s.envName, configpkg.EnvironmentTypeHost, err)
	}
	envProjectRoot = strings.TrimSpace(envProjectRoot)
	// Skipping the cluster/cloud/registry resolution below is what "no cluster
	// contact" means for host, but creating a new environment still asks for
	// the same confirmation every other type does — that step is local and
	// interactive, not cluster-touching.
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

// initDeployConfig resolves the Deploy block a new env is created with: a
// saved components selection when --components named one, else the zero value
// (no saved selection, so deploy falls back to the repo plan).
func initDeployConfig(params open.BootstrapInitParams) configpkg.EnvironmentDeployConfig {
	components, given := initComponentsOverride(params)
	if !given {
		return configpkg.EnvironmentDeployConfig{}
	}
	return configpkg.EnvironmentDeployConfig{Components: components}
}

// initComponentsOverride resolves the deploy.components override an init
// invocation named via --components, and whether one was named at all. A nil
// Components pointer means the invocation named nothing; a non-nil pointer
// (even to an empty slice) is an explicit request. That distinction is what
// lets an explicitly empty --components clear a saved selection instead of
// being indistinguishable from not passing the flag at all.
func initComponentsOverride(params open.BootstrapInitParams) ([]string, bool) {
	if params.Components == nil {
		return nil, false
	}
	return deploy.NormalizeComponentNames(*params.Components), true
}

// describeComponentsSetting renders a resolved components list for a trace
// line, naming explicitly when the list is an intentional clear rather than
// looking like nothing happened.
func describeComponentsSetting(components []string) string {
	if len(components) == 0 {
		return "(cleared — deploy now follows the repo k8s.deployments plan)"
	}
	return strings.Join(components, ",")
}

// seedInitContainerRegistries returns the marked list a new env is created with:
// a resolvable cluster: entry when the operator requested the in-cluster registry
// (--cluster-registry), otherwise the single static registry resolved from
// --container-registry / the prompt.
func seedInitContainerRegistries(params open.BootstrapInitParams, registry string) configpkg.ContainerRegistries {
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
	managedCloud, err := activity.ManagedCloudEnvironment(s.runner.Store, configpkg.EnvConfig{
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
	// A host env (whether it already was one, or this run just retyped it via
	// --type host) has no pod and no cluster to reconcile against — skip the
	// kubernetes-context/cloud-provider/registry steps below entirely rather
	// than let them resolve a context or ensure a namespace no host env has.
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
