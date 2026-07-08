package eruncommon

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/adrg/xdg"
	"gopkg.in/yaml.v3"
)

const (
	configRoot       = "erun"
	configFile       = "config.yaml"
	projectConfigDir = ".erun"
)

type ERunConfig struct {
	DefaultTenant   string
	CloudProviders  []CloudProviderConfig `yaml:"cloudproviders,omitempty"`
	CloudContexts   []CloudContextConfig  `yaml:"cloudcontexts,omitempty"`
	RuntimeRegistry RuntimeRegistryConfig `yaml:"runtimeregistry,omitempty"`
}

// RuntimeRegistryConfig lets operators running an internal mirror of `erun-devops`
// (Harbor, ECR, Artifactory, self-hosted GHCR) keep `erun version`'s image check
// off the public registries baked into the binary. All fields are optional; unset
// defaults to `ghcr.io/sophium/erun-devops` on the standard ghcr.io endpoints.
type RuntimeRegistryConfig struct {
	Namespace  string `yaml:"namespace,omitempty"`
	Repository string `yaml:"repository,omitempty"`
	// BaseURL default depends on the namespace: hub.docker.com for Docker Hub,
	// ghcr.io for ghcr.io namespaces.
	BaseURL string `yaml:"baseurl,omitempty"`
	// TokenURL is consulted only on the GHCR flow; defaults to https://ghcr.io.
	TokenURL string `yaml:"tokenurl,omitempty"`
}

type SSHDConfig struct {
	Enabled       bool                    `yaml:"enabled,omitempty"`
	LocalPort     int                     `yaml:"localport,omitempty"`
	PublicKeyPath string                  `yaml:"publickeypath,omitempty"`
	WorkspaceSync SSHDWorkspaceSyncConfig `yaml:"workspacesync,omitempty"`
}

type SSHDWorkspaceSyncConfig struct {
	Enabled   bool   `yaml:"enabled,omitempty"`
	LocalPath string `yaml:"localpath,omitempty"`
}

// EnvironmentType classifies an environment by where its worktree lives and
// whether builds happen inside it. See docs at /concepts/environment-types.
type EnvironmentType string

const (
	EnvironmentTypeLocalAgent  EnvironmentType = "local-agent"
	EnvironmentTypeRemoteAgent EnvironmentType = "remote-agent"
	EnvironmentTypeRuntime     EnvironmentType = "runtime"
)

// IsValid reports whether the value is one of the three canonical types.
// Empty is not valid; callers wanting "unset" should test against the zero
// value separately and then resolve via EnvConfig.ResolvedType.
func (t EnvironmentType) IsValid() bool {
	switch t {
	case EnvironmentTypeLocalAgent, EnvironmentTypeRemoteAgent, EnvironmentTypeRuntime:
		return true
	}
	return false
}

func (c SSHDWorkspaceSyncConfig) IsZero() bool {
	return !c.Enabled && strings.TrimSpace(c.LocalPath) == ""
}

func (c SSHDConfig) ResolvedLocalPort() int {
	if c.LocalPort > 0 {
		return c.LocalPort
	}
	return DefaultSSHLocalPort
}

type TenantConfig struct {
	Name                      string
	DefaultEnvironment        string
	APIURL                    string   `yaml:"api_url,omitempty" json:"apiUrl,omitempty"`
	CloudProviderAliases      []string `yaml:"cloudprovideraliases,omitempty" json:"cloudProviderAliases,omitempty"`
	PrimaryCloudProviderAlias string   `yaml:"primarycloudprovideralias,omitempty" json:"primaryCloudProviderAlias,omitempty"`
}

type EnvConfig struct {
	Name          string
	Type          EnvironmentType `yaml:"type,omitempty" json:"type,omitempty"`
	LocalRepoPath string          `yaml:"localrepopath,omitempty" json:"localRepoPath,omitempty"`
	// MountSource opts a runtime env into a mutable source worktree: the runtime
	// pod clones RepoURL at the deployed release ref (v<version>) into a
	// PVC-backed worktree on first boot, for real-time patching. It is a no-op
	// without RepoURL, and ignored for agent envs (which already carry source).
	// Default false keeps a runtime env sourceless (deploy-by-reference only).
	MountSource bool `yaml:"mountsource,omitempty" json:"mountSource,omitempty"`
	// RepoURL is the git remote the runtime pod clones when MountSource is set.
	RepoURL            string `yaml:"repourl,omitempty" json:"repoURL,omitempty"`
	KubernetesContext  string
	CloudProviderAlias string `yaml:"cloudprovideralias,omitempty"`
	// CloudProviderAliases attaches one cloud alias per provider type. The legacy
	// CloudProviderAlias scalar remains the AWS slot so pre-existing configs keep
	// working; an env carries at most one alias per provider type.
	CloudProviderAliases map[string]string `yaml:"cloudprovideraliases,omitempty" json:"cloudProviderAliases,omitempty"`
	ManagedCloud         bool              `yaml:"managedcloud,omitempty" json:"managedCloud,omitempty"`
	RuntimeVersion       string            `yaml:"runtimeversion,omitempty"`
	RuntimeRegistry      string            `yaml:"runtimeregistry,omitempty" json:"runtimeRegistry,omitempty"`
	// ContainerRegistries carries the marked registry list for environments
	// whose project config is not on the local machine (remote-agent and
	// runtime envs). Local-agent envs resolve their list from the project's
	// .erun/config.yaml instead; this field stays empty for them.
	ContainerRegistries ContainerRegistries `yaml:"containerregistries,omitempty" json:"containerRegistries,omitempty"`
	// RuntimeImage points the env's runtime pod at a custom image instead of the
	// published <registry>/erun-devops:<version> default. A full reference is used
	// verbatim; a bare name resolves against the env's registry and runtime version.
	RuntimeImage        string                  `yaml:"runtimeimage,omitempty" json:"runtimeImage,omitempty"`
	RuntimePod          RuntimePodResources     `yaml:"runtimepod,omitempty"`
	SSHD                SSHDConfig              `yaml:"sshd,omitempty"`
	Idle                EnvironmentIdleConfig   `yaml:"idle,omitempty"`
	Deploy              EnvironmentDeployConfig `yaml:"deploy,omitempty" json:"deploy,omitempty"`
	Claude              EnvironmentClaudeConfig `yaml:"claude,omitempty" json:"claude,omitempty"`
	AITool              string                  `yaml:"aitool,omitempty" json:"aiTool,omitempty"`
	LocalPortRangeStart int                     `yaml:"localportrangestart,omitempty" json:"localPortRangeStart,omitempty"`
	AutoStart           *bool                   `yaml:"autostart,omitempty" json:"autoStart,omitempty"`
	// Deprecated: host AWS credential delivery is now driven by whether an AWS
	// cloud alias is attached (HasAWSCloudAlias), not by a separate toggle —
	// attaching an alias means "act on my behalf here". The field is retained
	// so existing configs still parse; it no longer affects behavior.
	RemoteHostCredentials bool `yaml:"remotehostcredentials,omitempty" json:"remoteHostCredentials,omitempty"`
	// AutoUpgrade opts this env into the "Upgrade all" set, redeploying it to the
	// latest version for its UpgradeChannel when its RuntimeVersion lags.
	AutoUpgrade bool `yaml:"autoupgrade,omitempty" json:"autoUpgrade,omitempty"`
	// UpgradeChannel selects which release channel an upgrade targets: "stable"
	// (semver tags) or "snapshot" (*-snapshot-<timestamp> tags). Orthogonal to
	// Type; empty resolves from Type.
	UpgradeChannel string `yaml:"upgradechannel,omitempty" json:"upgradeChannel,omitempty"`
	// DisableBuildScript makes erun ignore any project build.sh for this env and
	// build docker/release directly. Default false keeps build.sh shadowing docker.
	DisableBuildScript bool `yaml:"disablebuildscript,omitempty" json:"disableBuildScript,omitempty"`
}

// ResolvedCloudAliases returns the env's attached cloud aliases keyed by provider
// type, with the legacy AWS-slot scalar folded in. An env holds at most one alias
// per provider type, so the runtime can carry, e.g., an AWS identity and a
// Cloudflare token at once.
func (c EnvConfig) ResolvedCloudAliases() map[string]string {
	resolved := make(map[string]string)
	if alias := strings.TrimSpace(c.CloudProviderAlias); alias != "" {
		resolved[cloudProviderTypeFromAlias(alias)] = alias
	}
	for providerType, alias := range c.CloudProviderAliases {
		providerType = strings.ToLower(strings.TrimSpace(providerType))
		alias = strings.TrimSpace(alias)
		if providerType == "" || alias == "" {
			continue
		}
		resolved[providerType] = alias
	}
	return resolved
}

// ResolvedType returns the env's type, or "" when unresolved. Older configs
// that carried only the legacy remote+snapshot pair are migrated to a concrete
// Type during YAML decode (see EnvConfig.UnmarshalYAML), so Type is the single
// source of truth here.
func (c EnvConfig) ResolvedType() EnvironmentType {
	if c.Type.IsValid() {
		return c.Type
	}
	return ""
}

// BuildsHere reports whether builds happen inside this env (local-agent and
// remote-agent build here; runtime envs only receive deploys). An env whose
// type is unresolved is treated as not building here.
func (c EnvConfig) BuildsHere() bool {
	return c.Type.IsValid() && c.Type != EnvironmentTypeRuntime
}

// RemoteWorktree reports whether the worktree lives outside the local
// machine (PVC for remote-agent, none for runtime). Local-agent mounts the
// worktree from the local filesystem via hostPath. An env whose type is
// unresolved is treated as not having a remote worktree.
func (c EnvConfig) RemoteWorktree() bool {
	return c.Type.IsValid() && c.Type != EnvironmentTypeLocalAgent
}

// MountsRuntimeSource reports whether this runtime env opts into a mutable
// source worktree — MountSource set together with a RepoURL to clone. Only
// runtime envs consult it; agent envs already carry source, so it is ignored
// for them. It gates both the PVC-backed worktree and the clone-at-release-ref
// wiring, so the two can never disagree.
func (c EnvConfig) MountsRuntimeSource() bool {
	return c.Type == EnvironmentTypeRuntime && c.MountSource && strings.TrimSpace(c.RepoURL) != ""
}

// HasAWSCloudAlias reports whether the env has an AWS cloud alias attached.
// An alias is a credential the operator already authenticated, so associating
// it with an env means "act on my behalf here" — that association alone (no
// separate toggle) drives delivery of the host AWS credentials into the env,
// mirroring how attaching a Cloudflare alias delivers its token.
func (c EnvConfig) HasAWSCloudAlias() bool {
	return strings.TrimSpace(c.ResolvedCloudAliases()[CloudProviderAWS]) != ""
}

// legacyEnvTypeFromRemoteSnapshot migrates configs written before the `type`
// field existed, mapping the old remote+snapshot pair to a concrete type. It must
// reproduce the old deciders exactly: a missing snapshot key meant "does not build
// here", and the one non-remote/non-building combo has no concrete type so its
// deciders stay false on both axes as before.
func legacyEnvTypeFromRemoteSnapshot(remote bool, snapshot *bool) EnvironmentType {
	buildsHere := snapshot != nil && *snapshot
	if remote {
		if buildsHere {
			return EnvironmentTypeRemoteAgent
		}
		return EnvironmentTypeRuntime
	}
	if buildsHere {
		return EnvironmentTypeLocalAgent
	}
	return ""
}

const (
	UpgradeChannelStable   = "stable"
	UpgradeChannelSnapshot = "snapshot"
)

// IsValidUpgradeChannel reports whether channel is one of the canonical
// channel values.
func IsValidUpgradeChannel(channel string) bool {
	switch strings.TrimSpace(channel) {
	case UpgradeChannelStable, UpgradeChannelSnapshot:
		return true
	}
	return false
}

// ResolvedUpgradeChannel returns the release channel an upgrade targets for
// this env. The explicit UpgradeChannel field is the source of truth when
// valid; otherwise it defaults from the resolved type — runtime envs track
// "stable", agent envs track "snapshot" (they iterate on snapshot builds) —
// and anything unresolved falls back to "stable".
func (c EnvConfig) ResolvedUpgradeChannel() string {
	if IsValidUpgradeChannel(c.UpgradeChannel) {
		return strings.TrimSpace(c.UpgradeChannel)
	}
	switch c.ResolvedType() {
	case EnvironmentTypeLocalAgent, EnvironmentTypeRemoteAgent:
		return UpgradeChannelSnapshot
	default:
		return UpgradeChannelStable
	}
}

// EffectiveLocalRepoPath returns the env's host-machine repo path. The legacy
// `repopath` key folds into LocalRepoPath on read, so callers wanting the host
// path should use this helper rather than reading the field directly.
func (c EnvConfig) EffectiveLocalRepoPath() string {
	return strings.TrimSpace(c.LocalRepoPath)
}

type ProjectEnvironmentConfig struct {
	ContainerRegistries ContainerRegistries `yaml:"containerregistries,omitempty"`
	Docker              ProjectDockerConfig `yaml:"docker,omitempty"`
	K8s                 ProjectK8sConfig    `yaml:"k8s,omitempty"`
}

// ProjectDockerConfig holds project-level docker settings per environment.
// Fingerprints maps an image name to its canonical content fingerprint as
// published by release CI, so fresh dev clones can promote pinned base images
// without rebuilding them while local Dockerfile edits still force a rebuild
// (the locally computed fingerprint diverges from the tagged hash).
type ProjectDockerConfig struct {
	Fingerprints map[string]string `yaml:"fingerprints,omitempty"`
}

func (c ProjectDockerConfig) IsZero() bool {
	return len(c.Fingerprints) == 0
}

type ReleaseConfig struct {
	MainBranch    string `yaml:"mainbranch,omitempty"`
	DevelopBranch string `yaml:"developbranch,omitempty"`
}

type ProjectConfig struct {
	ContainerRegistries ContainerRegistries                 `yaml:"containerregistries,omitempty"`
	Environments        map[string]ProjectEnvironmentConfig `yaml:"environments,omitempty"`
	Release             ReleaseConfig                       `yaml:"release,omitempty"`
	// Platform holds the per-instance erunpaas platform configuration; empty for
	// projects that do not run a platform deployment.
	Platform PlatformConfig `yaml:"platform,omitempty"`
	// Paths overrides where erun discovers the project's devops assets (docker/,
	// k8s/, terraform-<tenant>/, VERSION); empty keeps the conventional layout.
	Paths ProjectPathsConfig `yaml:"paths,omitempty"`
}

// K8sForEnvironment returns the k8s deploy plan declared for the given
// environment, or an empty plan when none exists.
func (c ProjectConfig) K8sForEnvironment(environment string) ProjectK8sConfig {
	environment = strings.TrimSpace(environment)
	if environment == "" || c.Environments == nil {
		return ProjectK8sConfig{}
	}
	envConfig, ok := c.Environments[environment]
	if !ok {
		return ProjectK8sConfig{}
	}
	return envConfig.K8s
}

// ProjectK8sConfig declares the deploy plan for `erun deploy` in this project.
// Deployments lists the steps in the order they must run; each step is a
// group of components to deploy in parallel. A step may be a single
// component name (scalar) or a sequence of names (parallel group).
type ProjectK8sConfig struct {
	Deployments []ProjectK8sDeploymentStep `yaml:"deployments,omitempty"`
}

func (c ProjectK8sConfig) IsZero() bool {
	return len(c.Deployments) == 0
}

// ProjectK8sDeploymentStep is one ordered step in the deploy plan. The
// Components slice always holds the parallel group; YAML unmarshaling lifts a
// scalar single-component form into a one-element slice so users can write
// either form interchangeably.
type ProjectK8sDeploymentStep struct {
	Components []string
}

func (s *ProjectK8sDeploymentStep) UnmarshalYAML(node *yaml.Node) error {
	if s == nil {
		return errors.New("nil ProjectK8sDeploymentStep")
	}
	switch node.Kind {
	case yaml.ScalarNode:
		value := strings.TrimSpace(node.Value)
		if value == "" {
			s.Components = nil
			return nil
		}
		s.Components = []string{value}
		return nil
	case yaml.SequenceNode:
		components := make([]string, 0, len(node.Content))
		for _, child := range node.Content {
			if child.Kind != yaml.ScalarNode {
				return errors.New("k8s.deployments parallel group must contain only component names")
			}
			value := strings.TrimSpace(child.Value)
			if value == "" {
				continue
			}
			components = append(components, value)
		}
		s.Components = components
		return nil
	default:
		return errors.New("k8s.deployments item must be a component name or a list of component names")
	}
}

// MarshalYAML emits the natural form: a scalar when the step has exactly one
// component, a flow-style sequence when it has multiple. This keeps
// round-tripped configs readable instead of forcing every step into a list.
func (s ProjectK8sDeploymentStep) MarshalYAML() (any, error) {
	if len(s.Components) == 1 {
		return s.Components[0], nil
	}
	return s.Components, nil
}

// DockerFingerprintsForEnvironment returns the image-name → fingerprint map for
// the given environment, or nil when none is set. Hash values are validated lazily
// at materialization, not load time, so a malformed entry surfaces in the build
// trace instead of breaking unrelated commands that read other config sections.
func (c ProjectConfig) DockerFingerprintsForEnvironment(environment string) map[string]string {
	environment = strings.TrimSpace(environment)
	if environment == "" || c.Environments == nil {
		return nil
	}
	envConfig, ok := c.Environments[environment]
	if !ok || len(envConfig.Docker.Fingerprints) == 0 {
		return nil
	}
	out := make(map[string]string, len(envConfig.Docker.Fingerprints))
	for name, hash := range envConfig.Docker.Fingerprints {
		name = strings.TrimSpace(name)
		hash = strings.TrimSpace(hash)
		if name == "" || hash == "" {
			continue
		}
		out[name] = hash
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (c ProjectConfig) NormalizedReleaseConfig() ReleaseConfig {
	config := c.Release
	if strings.TrimSpace(config.MainBranch) == "" {
		config.MainBranch = DefaultReleaseMainBranch
	}
	if strings.TrimSpace(config.DevelopBranch) == "" {
		config.DevelopBranch = DefaultReleaseDevelopBranch
	}
	return config
}

var (
	ErrNotInitialized     = errors.New("not initialized")
	ErrNoUserDataFolder   = errors.New("failed to obtain config file locations")
	ErrConfigCorrupted    = errors.New("config file cannot be unmarshaled")
	ErrFailedToSaveConfig = errors.New("could not save struct to yaml file")
	ErrNotInGitRepository = errors.New("cannot find git project")
)

func ERunConfigDir() (string, error) {
	configHome := strings.TrimSpace(xdg.ConfigHome)
	if configHome == "" {
		return "", ErrNoUserDataFolder
	}
	return filepath.Join(configHome, configRoot), nil
}

type ConfigStore struct{}

func (ConfigStore) LoadERunConfig() (ERunConfig, string, error) {
	return LoadERunConfig()
}

func (ConfigStore) SaveERunConfig(config ERunConfig) error {
	return SaveERunConfig(config)
}

func (ConfigStore) ListTenantConfigs() ([]TenantConfig, error) {
	return ListTenantConfigs()
}

func (ConfigStore) LoadTenantConfig(tenant string) (TenantConfig, string, error) {
	return LoadTenantConfig(tenant)
}

func (ConfigStore) SaveTenantConfig(config TenantConfig) error {
	return SaveTenantConfig(config)
}

func (ConfigStore) DeleteTenantConfig(tenant string) error {
	return DeleteTenantConfig(tenant)
}

func (ConfigStore) LoadEnvConfig(tenant, envName string) (EnvConfig, string, error) {
	return LoadEnvConfig(tenant, envName)
}

func (ConfigStore) ResolveEffectiveKubernetesContext(environment, configured string) string {
	return resolveEffectiveKubernetesContext(environment, configured, listKubernetesContextNames, currentKubernetesContextName)
}

func (ConfigStore) ResolveDeployKubernetesContext(environment, configured string) string {
	return resolveDeployKubernetesContext(environment, configured, currentKubernetesContextName)
}

func (ConfigStore) ListEnvConfigs(tenant string) ([]EnvConfig, error) {
	return ListEnvConfigs(tenant)
}

func (ConfigStore) SaveEnvConfig(tenant string, config EnvConfig) error {
	return SaveEnvConfig(tenant, config)
}

func (ConfigStore) DeleteEnvConfig(tenant, envName string) error {
	return DeleteEnvConfig(tenant, envName)
}

func (ConfigStore) LoadProjectConfig(projectRoot string) (ProjectConfig, string, error) {
	return LoadProjectConfig(projectRoot)
}

func (ConfigStore) SaveProjectConfig(projectRoot string, config ProjectConfig) error {
	return SaveProjectConfig(projectRoot, config)
}

func SaveERunConfig(config ERunConfig) error {
	configFilePath, err := xdg.ConfigFile(filepath.Join(configRoot, configFile))
	if err != nil {
		return ErrNoUserDataFolder
	}

	if err := os.MkdirAll(filepath.Dir(configFilePath), 0o755); err != nil {
		return ErrNoUserDataFolder
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return ErrFailedToSaveConfig
	}

	// Best-effort backup of the previous live file before overwrite: a backup
	// failure must not block the save, because the bug we guard against is data
	// loss and refusing to save when the backup dir is full would itself lose data.
	// Idempotent across repeated saves within one local day.
	_ = writeRootConfigBackupIfDue(configFilePath, timeNow)

	if err := writeFileAtomic(configFilePath, data, 0o644); err != nil {
		return ErrFailedToSaveConfig
	}

	return nil
}

func LoadERunConfig() (ERunConfig, string, error) {
	config := ERunConfig{}
	configFilePath, err := xdg.ConfigFile(filepath.Join(configRoot, configFile))
	if err != nil {
		return config, configFilePath, ErrNoUserDataFolder
	}

	data, err := os.ReadFile(configFilePath)
	if err != nil {
		return config, configFilePath, ErrNotInitialized
	}

	// A zero-length file is the residue of an interrupted non-atomic
	// write. Treating it as "successfully loaded empty config" lets
	// the next writer rebuild a fresh ERunConfig{} with only the
	// field it cares about, silently dropping every other section.
	// Surface it as corruption so callers route into the doctor
	// recovery path instead.
	if len(bytes.TrimSpace(data)) == 0 {
		return config, configFilePath, ErrConfigCorrupted
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, configFilePath, ErrConfigCorrupted
	}

	return config, configFilePath, nil
}

func SaveTenantConfig(config TenantConfig) error {
	config = NormalizeTenantConfig(config)
	configFilePath, err := xdg.ConfigFile(filepath.Join(configRoot, config.Name, configFile))
	if err != nil {
		return ErrNoUserDataFolder
	}

	if err := os.MkdirAll(filepath.Dir(configFilePath), 0o755); err != nil {
		return ErrNoUserDataFolder
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return ErrFailedToSaveConfig
	}

	if err := writeFileAtomic(configFilePath, data, 0o644); err != nil {
		return ErrFailedToSaveConfig
	}

	return nil
}

func NormalizeTenantConfig(config TenantConfig) TenantConfig {
	config.Name = strings.TrimSpace(config.Name)
	config.DefaultEnvironment = strings.TrimSpace(config.DefaultEnvironment)
	config.APIURL = strings.TrimSpace(config.APIURL)
	config.CloudProviderAliases, config.PrimaryCloudProviderAlias = NormalizeTenantCloudProviderAliases(config.CloudProviderAliases, config.PrimaryCloudProviderAlias)
	return config
}

func DeleteTenantConfig(tenant string) error {
	configFilePath, err := xdg.ConfigFile(filepath.Join(configRoot, tenant, configFile))
	if err != nil {
		return ErrNoUserDataFolder
	}

	if err := os.RemoveAll(filepath.Dir(configFilePath)); err != nil {
		return ErrFailedToSaveConfig
	}
	return nil
}

func LoadTenantConfig(tenant string) (TenantConfig, string, error) {
	config := TenantConfig{}
	configFilePath, err := xdg.ConfigFile(filepath.Join(configRoot, tenant, configFile))
	if err != nil {
		return config, configFilePath, ErrNoUserDataFolder
	}

	data, err := os.ReadFile(configFilePath)
	if err != nil {
		return config, configFilePath, ErrNotInitialized
	}

	if len(bytes.TrimSpace(data)) == 0 {
		return config, configFilePath, ErrConfigCorrupted
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, configFilePath, ErrConfigCorrupted
	}

	return NormalizeTenantConfig(config), configFilePath, nil
}

func ListTenantConfigs() ([]TenantConfig, error) {
	configFilePath, err := xdg.ConfigFile(filepath.Join(configRoot, configFile))
	if err != nil {
		return nil, ErrNoUserDataFolder
	}

	entries, err := os.ReadDir(filepath.Dir(configFilePath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	tenants := make([]TenantConfig, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		tenantConfig, _, err := LoadTenantConfig(entry.Name())
		if errors.Is(err, ErrNotInitialized) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if tenantConfig.Name == "" {
			tenantConfig.Name = entry.Name()
		}
		tenants = append(tenants, tenantConfig)
	}

	sort.Slice(tenants, func(i, j int) bool {
		return tenants[i].Name < tenants[j].Name
	})

	return tenants, nil
}

func SaveEnvConfig(tenant string, config EnvConfig) error {
	configFilePath, err := xdg.ConfigFile(filepath.Join(configRoot, tenant, config.Name, configFile))
	if err != nil {
		return ErrNoUserDataFolder
	}

	if err := os.MkdirAll(filepath.Dir(configFilePath), 0o755); err != nil {
		return ErrNoUserDataFolder
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return ErrFailedToSaveConfig
	}

	// Best-effort backup of the previous live env config before overwrite, like
	// SaveERunConfig. Idempotent within one local day; it guards against a wrong
	// value (e.g. a type silently resolved to "runtime" on a remote-agent env)
	// being persisted with no way back. A backup failure must not block the save,
	// since the bug guarded against is data loss and refusing to save when the
	// backup dir is unwritable would be worse.
	_ = writeEnvConfigBackupIfDue(configFilePath, timeNow)

	if err := writeFileAtomic(configFilePath, data, 0o644); err != nil {
		return ErrFailedToSaveConfig
	}

	return nil
}

func DeleteEnvConfig(tenant, envName string) error {
	configFilePath, err := xdg.ConfigFile(filepath.Join(configRoot, tenant, envName, configFile))
	if err != nil {
		return ErrNoUserDataFolder
	}

	if err := os.RemoveAll(filepath.Dir(configFilePath)); err != nil {
		return ErrFailedToSaveConfig
	}
	return nil
}

func LoadEnvConfig(tenant, envName string) (EnvConfig, string, error) {
	config := EnvConfig{}
	configFilePath, err := xdg.ConfigFile(filepath.Join(configRoot, tenant, envName, configFile))
	if err != nil {
		return config, configFilePath, ErrNoUserDataFolder
	}

	data, err := os.ReadFile(configFilePath)
	if err != nil {
		return config, configFilePath, ErrNotInitialized
	}

	if len(bytes.TrimSpace(data)) == 0 {
		return config, configFilePath, ErrConfigCorrupted
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, configFilePath, ErrConfigCorrupted
	}

	return config, configFilePath, nil
}

func ListEnvConfigs(tenant string) ([]EnvConfig, error) {
	configFilePath, err := xdg.ConfigFile(filepath.Join(configRoot, tenant, configFile))
	if err != nil {
		return nil, ErrNoUserDataFolder
	}

	entries, err := os.ReadDir(filepath.Dir(configFilePath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	envs := make([]EnvConfig, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		envConfig, _, err := LoadEnvConfig(tenant, entry.Name())
		if errors.Is(err, ErrNotInitialized) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if envConfig.Name == "" {
			envConfig.Name = entry.Name()
		}
		envs = append(envs, envConfig)
	}

	sort.Slice(envs, func(i, j int) bool {
		return envs[i].Name < envs[j].Name
	})

	return envs, nil
}

func SaveProjectConfig(projectRoot string, config ProjectConfig) error {
	configFilePath, err := projectConfigPath(projectRoot)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(configFilePath), 0o755); err != nil {
		return ErrFailedToSaveConfig
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return ErrFailedToSaveConfig
	}

	if err := writeFileAtomic(configFilePath, data, 0o644); err != nil {
		return ErrFailedToSaveConfig
	}

	return nil
}

func LoadProjectConfig(projectRoot string) (ProjectConfig, string, error) {
	config := ProjectConfig{}
	configFilePath, err := projectConfigPath(projectRoot)
	if err != nil {
		return config, "", err
	}

	data, err := os.ReadFile(configFilePath)
	if err != nil {
		return config, configFilePath, ErrNotInitialized
	}

	if len(bytes.TrimSpace(data)) == 0 {
		return config, configFilePath, ErrConfigCorrupted
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, configFilePath, fmt.Errorf("%w: %v", ErrConfigCorrupted, err)
	}

	return config, configFilePath, nil
}

func FindProjectRoot() (string, string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	return FindProjectRootFromDir(dir)
}

func FindProjectRootFromDir(dir string) (string, string, error) {
	dir = filepath.Clean(dir)
	for {
		gitDir := filepath.Join(dir, ".git")
		if _, err := os.Stat(gitDir); err == nil {
			repoName := filepath.Base(dir)
			return repoName, dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", ErrNotInGitRepository
		}
		dir = parent
	}
}

func projectConfigPath(projectRoot string) (string, error) {
	if projectRoot == "" {
		return "", ErrNotInGitRepository
	}
	return filepath.Join(filepath.Clean(projectRoot), projectConfigDir, configFile), nil
}

// writeFileAtomic writes via a sibling temp file, fsync, then rename so a crash
// or kill mid-write leaves either the previous contents or no change at all —
// never a 0-byte or partially written file. The rename is atomic because the temp
// file shares the destination's directory, and thus its filesystem.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	committed = true
	return nil
}
