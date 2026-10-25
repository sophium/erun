package setup

import (
	"fmt"
	"slices"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/deploy/runtimeimage"
)

// updateExistingEnvSettings applies only supplied settings, whatever the
// resolved type; omitted ones are left alone, never reset to defaults.
func (s *bootstrapRunState) updateExistingEnvSettings() error {
	if s.result.CreatedEnvConfig {
		return nil
	}
	s.applyEnvRuntimeVersion()
	s.applyEnvSetting("runtime image", runtimeimage.StripRuntimeImageTag(s.params.RuntimeImage), &s.envConfig.RuntimeImage)
	// Recording the registry before this run's deploy is how re-init breaks a
	// chart-resolution deadlock.
	s.applyEnvSetting("runtime registry", s.params.RuntimeRegistry, &s.envConfig.RuntimeRegistry)
	s.applyEnvImagePullSecrets()
	s.applyEnvRuntimePod()
	s.applyEnvRuntimeDindPod()
	s.applyEnvDeployComponents()
	return s.applyEnvType()
}

func (s *bootstrapRunState) applyEnvSetting(name, param string, recorded *string) {
	value := strings.TrimSpace(param)
	if value == "" {
		s.traceEnvSettingKept(name, *recorded)
		return
	}
	if *recorded == value {
		s.runner.Context.Trace("init: " + name + " already " + value)
		return
	}
	*recorded = value
	s.envConfigChanged = true
	s.runner.Context.Trace("init: " + name + " set to " + value)
}

func (s *bootstrapRunState) traceEnvSettingKept(name, recorded string) {
	if strings.TrimSpace(recorded) == "" {
		return
	}
	s.runner.Context.Trace("init: " + name + " not given; keeping " + recorded)
}

// applyEnvRuntimeVersion leaves the env's version alone unless --version is
// given or the env has none: upgrading is deploy's job.
func (s *bootstrapRunState) applyEnvRuntimeVersion() {
	s.applyEnvSetting("runtime version", s.params.RuntimeVersion, &s.envConfig.RuntimeVersion)
	if strings.TrimSpace(s.envConfig.RuntimeVersion) != "" {
		return
	}
	fallback := strings.TrimSpace(s.params.RuntimeVersionDefault)
	if fallback == "" {
		return
	}
	s.envConfig.RuntimeVersion = fallback
	s.envConfigChanged = true
	s.runner.Context.Trace("init: env records no runtime version; adopting " + fallback)
}

func (s *bootstrapRunState) applyEnvImagePullSecrets() {
	pullSecrets := configpkg.NormalizeImagePullSecrets(s.params.ImagePullSecrets)
	if len(pullSecrets) == 0 {
		s.traceEnvSettingKept("image pull secrets", strings.Join(s.envConfig.ImagePullSecrets, ","))
		return
	}
	if slices.Equal(pullSecrets, s.envConfig.ImagePullSecrets) {
		s.runner.Context.Trace("init: image pull secrets already " + strings.Join(pullSecrets, ","))
		return
	}
	s.envConfig.ImagePullSecrets = pullSecrets
	s.envConfigChanged = true
	s.runner.Context.Trace("init: image pull secrets set to " + strings.Join(pullSecrets, ","))
}

// applyEnvRuntimePod merges raw flags: normalized defaults would look like an
// operator asking for them and reset unrelated limits.
func (s *bootstrapRunState) applyEnvRuntimePod() {
	desired := s.envConfig.RuntimePod
	if cpu := strings.TrimSpace(s.params.RuntimePod.CPU); cpu != "" {
		desired.CPU = cpu
	}
	if memory := strings.TrimSpace(s.params.RuntimePod.Memory); memory != "" {
		desired.Memory = memory
	}
	if desired == s.envConfig.RuntimePod {
		s.traceEnvSettingKept("runtime pod resources", formatRuntimePodResources(s.envConfig.RuntimePod))
		return
	}
	s.envConfig.RuntimePod = configpkg.NormalizeRuntimePodResources(desired)
	s.envConfigChanged = true
	s.runner.Context.Trace("init: runtime pod resources set to " + formatRuntimePodResources(s.envConfig.RuntimePod))
}

func (s *bootstrapRunState) applyEnvRuntimeDindPod() {
	desired := s.envConfig.RuntimeDindPod
	if cpu := strings.TrimSpace(s.params.RuntimeDindPod.CPU); cpu != "" {
		desired.CPU = cpu
	}
	if memory := strings.TrimSpace(s.params.RuntimeDindPod.Memory); memory != "" {
		desired.Memory = memory
	}
	if desired == s.envConfig.RuntimeDindPod {
		s.traceEnvSettingKept("erun-dind sidecar resources", formatRuntimeDindPodResources(s.envConfig.RuntimeDindPod))
		return
	}
	s.envConfig.RuntimeDindPod = configpkg.NormalizeRuntimeDindPodResources(desired)
	s.envConfigChanged = true
	s.runner.Context.Trace("init: erun-dind sidecar resources set to " + formatRuntimeDindPodResources(s.envConfig.RuntimeDindPod))
}

// applyEnvDeployComponents overwrites the saved selection when --components is
// given, even with an empty list.
func (s *bootstrapRunState) applyEnvDeployComponents() {
	components, given := initComponentsOverride(s.params)
	if !given {
		s.traceEnvSettingKept("deploy components", strings.Join(s.envConfig.Deploy.Components, ","))
		return
	}
	if slices.Equal(components, s.envConfig.Deploy.Components) {
		s.runner.Context.Trace("init: deploy components already " + describeComponentsSetting(components))
		return
	}
	s.envConfig.Deploy.Components = components
	s.envConfigChanged = true
	s.runner.Context.Trace("init: deploy components set to " + describeComponentsSetting(components))
}

// applyEnvType retypes only when --type was named; the default is not a request.
func (s *bootstrapRunState) applyEnvType() error {
	requested, explicit := s.params.RequestedType()
	current := s.envConfig.ResolvedType()
	if !explicit {
		s.runner.Context.Trace("init: --type not given; keeping env type " + describeEnvType(current))
		return nil
	}
	if current == requested {
		s.runner.Context.Trace("init: env type already " + describeEnvType(requested))
		return nil
	}
	if err := s.adoptLocalRepoPathForType(requested); err != nil {
		return err
	}
	s.envConfig.Type = requested
	s.envConfigChanged = true
	s.runner.Context.Trace("init: env type " + describeEnvType(current) + " -> " + describeEnvType(requested))
	return nil
}

// adoptLocalRepoPathForType refuses a retype to local-agent or host with no host
// path to adopt: a remote env's path exists only in its pod.
func (s *bootstrapRunState) adoptLocalRepoPathForType(requested configpkg.EnvironmentType) error {
	if requested != configpkg.EnvironmentTypeLocalAgent && requested != configpkg.EnvironmentTypeHost {
		return nil
	}
	projectRoot, err := s.envProjectRoot()
	if err != nil {
		return err
	}
	if err := configpkg.ValidateHostRepoPath(requested, projectRoot); err != nil {
		return fmt.Errorf("cannot change %s/%s to type %s: %w", s.tenant, s.envName, requested, err)
	}
	projectRoot = strings.TrimSpace(projectRoot)
	if s.envConfig.LocalRepoPath == projectRoot {
		return nil
	}
	s.envConfig.LocalRepoPath = projectRoot
	s.envConfigChanged = true
	s.runner.Context.Trace("init: local repo path set to " + projectRoot)
	return nil
}

func describeEnvType(envType configpkg.EnvironmentType) string {
	if !envType.IsValid() {
		return "unset"
	}
	return `"` + string(envType) + `"`
}

func formatRuntimePodResources(resources model.RuntimePodResources) string {
	resources = configpkg.NormalizeRuntimePodResources(resources)
	return "cpu=" + resources.CPU + " memory=" + resources.Memory
}

func formatRuntimeDindPodResources(resources model.RuntimePodResources) string {
	resources = configpkg.NormalizeRuntimeDindPodResources(resources)
	return "cpu=" + resources.CPU + " memory=" + resources.Memory
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
	managedCloud, err := activity.ManagedCloudEnvironment(s.runner.Store, s.envConfig)
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
		desired := configpkg.ClusterContainerRegistries(*s.params.ClusterRegistry)
		if !s.envConfig.ContainerRegistries.Equal(desired) {
			s.envConfig.ContainerRegistries = desired
			s.envConfigChanged = true
		}
		return nil
	}
	current, _ := s.envConfig.ContainerRegistries.BuildRegistry()
	containerRegistry, err := s.runner.ResolveContainerRegistry(s.params, s.tenant, s.envName, projectRoot, current, false)
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
		s.envConfig.ContainerRegistries = configpkg.SingleContainerRegistries(containerRegistry)
		s.envConfigChanged = true
	}
	return nil
}
