package setup

import (
	"fmt"
	"slices"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// updateExistingEnvSettings reconciles an already-initialized env against the
// settings this invocation supplied. Every setting here describes the runtime
// pod, which an env of any type has, so it is applied on the strength of having
// been supplied — not on the type the invocation happens to resolve to. A
// setting the invocation left out is left alone rather than reset to a default,
// so re-running init to change one thing never rewrites the rest.
//
// A freshly created env needs none of this: createEnvConfig wrote it from these
// same params moments ago, so there is nothing to reconcile and nothing to keep.
func (s *bootstrapRunState) updateExistingEnvSettings() error {
	if s.result.CreatedEnvConfig {
		return nil
	}
	s.applyEnvRuntimeVersion()
	s.applyEnvSetting("runtime image", deploy.StripRuntimeImageTag(s.params.RuntimeImage), &s.envConfig.RuntimeImage)
	// Re-running init is how an env deadlocked on chart resolution gets out: the
	// registry lands on the config before this run's own deploy resolves a chart.
	s.applyEnvSetting("runtime registry", s.params.RuntimeRegistry, &s.envConfig.RuntimeRegistry)
	s.applyEnvImagePullSecrets()
	s.applyEnvRuntimePod()
	s.applyEnvRuntimeDindPod()
	s.applyEnvDeployComponents()
	return s.applyEnvType()
}

// applyEnvSetting records a supplied value for one string setting, tracing the
// decision either way so a dry run shows what a re-init would touch and what it
// would deliberately leave as it found it.
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

// traceEnvSettingKept records that a stored setting survived this run untouched
// because the invocation never named it. It is the line that tells an operator
// reading a dry run the difference between "unchanged" and "silently dropped".
func (s *bootstrapRunState) traceEnvSettingKept(name, recorded string) {
	if strings.TrimSpace(recorded) == "" {
		return
	}
	s.runner.Context.Trace("init: " + name + " not given; keeping " + recorded)
}

// applyEnvRuntimeVersion honours --version, and otherwise leaves the env on the
// version it is running: an env is upgraded by deploying a version, not by an
// init that was about something else. The transport's fallback fills in only for
// an env carrying no version at all, which has nothing to deploy without one.
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

// applyEnvRuntimePod merges the supplied limits onto the recorded ones so
// naming only the CPU leaves the env's memory where it was. Params carry the
// raw flags here, unnormalized, because normalized defaults are indistinguishable
// from an operator asking for the defaults — and that is how a re-init meant to
// add a pull secret used to reset an env's pod resources.
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

// applyEnvRuntimeDindPod mirrors applyEnvRuntimePod for the erun-dind sidecar:
// merge the supplied limits onto the recorded ones, using the raw
// (unnormalized) flag values so a re-init that only touches an unrelated
// setting cannot reset the sidecar's resources to their defaults.
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

// applyEnvDeployComponents honours --components on an existing env: given, it
// overwrites the saved selection outright — including with an empty list,
// which clears it and returns the env to its repo k8s.deployments plan;
// not given, the saved selection (if any) is left exactly as it was.
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

// applyEnvType moves an existing env between types, in either direction and
// between any two of them — an env that cannot be retyped can only be deleted
// and rebuilt. It moves only when the operator named a type: the default an
// omitted --type resolves to is not a request, and acting on it would retype an
// env for saying nothing about it.
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

// adoptLocalRepoPathForType re-resolves the host repo path when an env becomes
// a local-agent (whose worktree is hostPath-mounted from it) or a host env
// (whose worktree IS it, with no pod to mount it into). The path a remote env
// carries names an in-pod directory that does not exist on this machine, so
// without one to adopt the retype is refused rather than written: an env
// recorded as local-agent or host with nothing to use would fail later with a
// message about the missing path rather than about the flag that caused it.
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

func formatRuntimePodResources(resources configpkg.RuntimePodResources) string {
	resources = configpkg.NormalizeRuntimePodResources(resources)
	return "cpu=" + resources.CPU + " memory=" + resources.Memory
}

func formatRuntimeDindPodResources(resources configpkg.RuntimePodResources) string {
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
