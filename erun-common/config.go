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

// RuntimeRegistryConfig overrides where `erun version` checks for available
// runtime images. Operators running internal mirrors of `erun-devops`
// (Harbor, ECR, Artifactory, a self-hosted GHCR) point Namespace and
// BaseURL at their mirror so the version check stops talking to the
// public registries baked into the binary.
//
// All fields are optional. When unset, defaults are
// `ghcr.io/sophium/erun-devops` with the standard ghcr.io endpoints, which
// matches the previous hardcoded behavior.
type RuntimeRegistryConfig struct {
	Namespace  string `yaml:"namespace,omitempty"`
	Repository string `yaml:"repository,omitempty"`
	// BaseURL overrides the registry HTTP endpoint. Default depends on the
	// resolved namespace prefix: `https://hub.docker.com` for Docker Hub
	// namespaces, `https://ghcr.io` for `ghcr.io/...` namespaces.
	BaseURL string `yaml:"baseurl,omitempty"`
	// TokenURL overrides the GHCR token endpoint. Default `https://ghcr.io`.
	// Only consulted on the GHCR flow.
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
	ProjectRoot               string
	Name                      string
	DefaultEnvironment        string
	APIURL                    string   `yaml:"api_url,omitempty" json:"apiUrl,omitempty"`
	CloudProviderAliases      []string `yaml:"cloudprovideraliases,omitempty" json:"cloudProviderAliases,omitempty"`
	PrimaryCloudProviderAlias string   `yaml:"primarycloudprovideralias,omitempty" json:"primaryCloudProviderAlias,omitempty"`
	Remote                    bool     `yaml:"remote,omitempty"`
	Snapshot                  *bool    `yaml:"snapshot,omitempty"`
}

type EnvConfig struct {
	Name               string
	Type               EnvironmentType `yaml:"type,omitempty" json:"type,omitempty"`
	LocalRepoPath      string          `yaml:"localrepopath,omitempty" json:"localRepoPath,omitempty"`
	RepoPath           string          `yaml:"repopath,omitempty"`
	KubernetesContext  string
	ContainerRegistry  string
	CloudProviderAlias string `yaml:"cloudprovideralias,omitempty"`
	ManagedCloud       bool   `yaml:"managedcloud,omitempty" json:"managedCloud,omitempty"`
	RuntimeVersion     string `yaml:"runtimeversion,omitempty"`
	RuntimeRegistry    string `yaml:"runtimeregistry,omitempty" json:"runtimeRegistry,omitempty"`
	// RuntimeImage points the env's runtime pod at a custom image instead
	// of the published <registry>/erun-devops:<version> default. A full
	// reference ("ghcr.io/acme/my-devops:1.2.3") is used verbatim; a bare
	// name resolves against the env's registry and runtime version. Set by
	// `erun init --runtime-image` and carried to the published chart as
	// imageOverrides.erun-devops on every deploy.
	RuntimeImage          string                  `yaml:"runtimeimage,omitempty" json:"runtimeImage,omitempty"`
	RuntimePod            RuntimePodResources     `yaml:"runtimepod,omitempty"`
	SSHD                  SSHDConfig              `yaml:"sshd,omitempty"`
	Idle                  EnvironmentIdleConfig   `yaml:"idle,omitempty"`
	Claude                EnvironmentClaudeConfig `yaml:"claude,omitempty" json:"claude,omitempty"`
	AITool                string                  `yaml:"aitool,omitempty" json:"aiTool,omitempty"`
	Remote                bool                    `yaml:"remote,omitempty"`
	Snapshot              *bool                   `yaml:"snapshot,omitempty"`
	LocalPortRangeStart   int                     `yaml:"localportrangestart,omitempty" json:"localPortRangeStart,omitempty"`
	AutoStart             *bool                   `yaml:"autostart,omitempty" json:"autoStart,omitempty"`
	RemoteHostCredentials bool                    `yaml:"remotehostcredentials,omitempty" json:"remoteHostCredentials,omitempty"`
	// AutoUpgrade opts this env into the "Upgrade all" set: `erun upgrade`
	// (and the desktop's Upgrade-all action) redeploy it to the latest
	// version for its UpgradeChannel when the current RuntimeVersion lags.
	AutoUpgrade bool `yaml:"autoupgrade,omitempty" json:"autoUpgrade,omitempty"`
	// UpgradeChannel selects which release channel an upgrade targets:
	// "stable" (semver tags) or "snapshot" (*-snapshot-<timestamp> tags).
	// It is orthogonal to Type (build location); empty resolves via
	// ResolvedUpgradeChannel from Type.
	UpgradeChannel string `yaml:"upgradechannel,omitempty" json:"upgradeChannel,omitempty"`
}

func (c TenantConfig) SnapshotEnabled() bool {
	if c.Snapshot == nil {
		return false
	}
	return *c.Snapshot
}

func (c *TenantConfig) SetSnapshot(enabled bool) {
	if c == nil {
		return
	}
	value := enabled
	c.Snapshot = &value
}

func (c EnvConfig) SnapshotEnabled() bool {
	if c.Snapshot == nil {
		return false
	}
	return *c.Snapshot
}

func (c *EnvConfig) SetSnapshot(enabled bool) {
	if c == nil {
		return
	}
	value := enabled
	c.Snapshot = &value
}

// ResolvedType returns the env's type. When Type is set it is the source of
// truth. When unset, derives the type from the legacy Remote+Snapshot pair
// only when both signals are present per the documented truth table at
// /reference/configuration#planned-changes — otherwise returns "" so callers
// fall back to the legacy predicates (BuildsHere, RemoteWorktree) without
// silently reclassifying ambiguous envs:
//
//	type set                                  → the set value
//	type unset, snapshot==nil                 → "" (unresolved; legacy fallback)
//	type unset, remote=false, snapshot=true   → local-agent
//	type unset, remote=true,  snapshot=true   → remote-agent
//	type unset, remote=true,  snapshot=false  → runtime
//	type unset, remote=false, snapshot=false  → "" (ambiguous local env)
func (c EnvConfig) ResolvedType() EnvironmentType {
	if c.Type.IsValid() {
		return c.Type
	}
	if c.Snapshot == nil {
		return ""
	}
	snapshot := *c.Snapshot
	if !c.Remote {
		if snapshot {
			return EnvironmentTypeLocalAgent
		}
		return ""
	}
	if snapshot {
		return EnvironmentTypeRemoteAgent
	}
	return EnvironmentTypeRuntime
}

// BuildsHere reports whether builds happen inside this env (local-agent and
// remote-agent build here; runtime envs only receive deploys). When Type is
// explicitly set it is the source of truth; otherwise the result matches the
// legacy SnapshotEnabled() value so callers preserve today's behavior.
func (c EnvConfig) BuildsHere() bool {
	if c.Type.IsValid() {
		return c.Type != EnvironmentTypeRuntime
	}
	return c.SnapshotEnabled()
}

// RemoteWorktree reports whether the worktree lives outside the local
// machine (PVC for remote-agent, none for runtime). Local-agent mounts the
// worktree from the local filesystem via hostPath. When Type is explicitly
// set it is the source of truth; otherwise the result matches the legacy
// Remote bool so callers preserve today's behavior.
func (c EnvConfig) RemoteWorktree() bool {
	if c.Type.IsValid() {
		return c.Type != EnvironmentTypeLocalAgent
	}
	return c.Remote
}

// UpgradeChannel values for EnvConfig.UpgradeChannel / ResolvedUpgradeChannel.
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

// EffectiveLocalRepoPath returns the new-shape LocalRepoPath when set,
// falling back to the legacy RepoPath. Callers that need the path on the
// host machine (chart worktreeHostPath, cwd→tenant matcher) should use this
// helper instead of reading either field directly.
func (c EnvConfig) EffectiveLocalRepoPath() string {
	if path := strings.TrimSpace(c.LocalRepoPath); path != "" {
		return path
	}
	return strings.TrimSpace(c.RepoPath)
}

type ProjectEnvironmentConfig struct {
	ContainerRegistry string              `yaml:"containerregistry,omitempty"`
	Docker            ProjectDockerConfig `yaml:"docker,omitempty"`
	K8s               ProjectK8sConfig    `yaml:"k8s,omitempty"`
}

// ProjectDockerConfig holds project-level docker settings per environment.
// Fingerprints maps an image name (the build-context dir name, e.g.
// "erun-ubuntu") to its canonical content fingerprint as published by release
// CI. When set, the incremental build flow pulls <image>:<VERSION> from the
// registry and tags it locally as <image>:fp-<configured>-<arch> before
// running fingerprint promotion. This lets fresh dev clones promote pinned
// bases without rebuilding them, while local Dockerfile edits still trigger a
// rebuild because the locally-computed fingerprint diverges from the tagged
// hash.
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
	ContainerRegistry string                              `yaml:"containerregistry,omitempty"`
	Environments      map[string]ProjectEnvironmentConfig `yaml:"environments,omitempty"`
	Release           ReleaseConfig                       `yaml:"release,omitempty"`
}

// K8sForEnvironment returns the k8s deploy plan declared for the given
// environment in this project config, or an empty plan when none exists.
// Mirrors ContainerRegistryForEnvironment in shape so callers can resolve a
// plan by environment without reaching into the Environments map.
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

// DockerFingerprintsForEnvironment returns the configured image-name →
// fingerprint map for the given environment, or nil when none is set. Empty
// keys and values are dropped. Hash values are validated lazily by the
// materialization step rather than at load time, so a malformed entry
// surfaces in the build trace instead of breaking unrelated commands that
// just want to read other parts of the config.
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

func (c ProjectConfig) ContainerRegistryForEnvironment(environment string) string {
	environment = strings.TrimSpace(environment)
	if environment != "" && c.Environments != nil {
		if envConfig, ok := c.Environments[environment]; ok {
			if registry := strings.TrimSpace(envConfig.ContainerRegistry); registry != "" {
				return registry
			}
		}
	}

	return strings.TrimSpace(c.ContainerRegistry)
}

func (c *ProjectConfig) SetContainerRegistryForEnvironment(environment, registry string) {
	environment = strings.TrimSpace(environment)
	registry = strings.TrimSpace(registry)

	if environment == "" {
		c.ContainerRegistry = registry
		return
	}

	if registry == "" {
		if c.Environments != nil {
			delete(c.Environments, environment)
			if len(c.Environments) == 0 {
				c.Environments = nil
			}
		}
		return
	}

	if registry == strings.TrimSpace(c.ContainerRegistry) {
		if c.Environments != nil {
			delete(c.Environments, environment)
			if len(c.Environments) == 0 {
				c.Environments = nil
			}
		}
		return
	}

	if c.Environments == nil {
		c.Environments = make(map[string]ProjectEnvironmentConfig)
	}

	envConfig := c.Environments[environment]
	envConfig.ContainerRegistry = registry
	c.Environments[environment] = envConfig
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

	// Capture today's snapshot of the previous live file before we
	// overwrite it. Best-effort: a backup failure must not block the
	// save (the bug we are guarding against is data loss, and refusing
	// to save when the backup directory is full would itself cause
	// data loss). The implementation is idempotent on repeated saves
	// within one local day.
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

// NormalizeEnvConfig populates the new-shape fields (Type, LocalRepoPath)
// from legacy fields when the new shape is unset. Idempotent: re-running it
// on an already-normalized config is a no-op. Called by LoadEnvConfig so
// loaded envs always expose a populated Type, and so subsequent saves write
// both old and new shapes — letting old binaries read the file while the
// migration window is open.
func NormalizeEnvConfig(config EnvConfig) EnvConfig {
	if !config.Type.IsValid() {
		config.Type = config.ResolvedType()
	}
	if strings.TrimSpace(config.LocalRepoPath) == "" && config.Type == EnvironmentTypeLocalAgent {
		config.LocalRepoPath = strings.TrimSpace(config.RepoPath)
	}
	return config
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

	return NormalizeEnvConfig(config), configFilePath, nil
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

// writeFileAtomic writes data to a sibling temp file in the same
// directory as path, fsyncs the contents, then renames it over the
// destination. The rename is atomic on POSIX filesystems when source
// and destination share a filesystem, which is guaranteed here because
// the temp file is created in filepath.Dir(path). A crash or process
// kill between the create and the rename leaves either the previous
// contents intact or no change at all — never a 0-byte or partially
// written file. Replaces the previous os.WriteFile (O_TRUNC|O_WRONLY)
// path which produced exactly that hazard on interruption.
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
