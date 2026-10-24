package config

import (
	"errors"
	"strings"
)

const (
	ConfigRoot       = "erun"
	ConfigFile       = "config.yaml"
	ProjectConfigDir = ".erun"
)

type ERunConfig struct {
	DefaultTenant   string
	CloudProviders  []CloudProviderConfig `yaml:"cloudproviders,omitempty"`
	CloudContexts   []CloudContextConfig  `yaml:"cloudcontexts,omitempty"`
	RuntimeRegistry RuntimeRegistryConfig `yaml:"runtimeregistry,omitempty"`
	// Orchestrators are the persisted host-side AI orchestrator definitions.
	Orchestrators []OrchestratorConfig `yaml:"orchestrators,omitempty" json:"orchestrators,omitempty"`
	// Whip overrides the pacing nudge defaults; nil keeps the defaults.
	Whip *WhipConfigOverride `yaml:"whip,omitempty" json:"whip,omitempty"`
	// Execution holds per-operation subprocess/library execution mode overrides.
	Execution ExecutionConfig `yaml:"execution,omitempty" json:"execution,omitempty"`
	// OpenRouter is the erun-level gateway catalog; nil means no gateway.
	OpenRouter *OpenRouterConfig `yaml:"openrouter,omitempty" json:"openRouter,omitempty"`
}

// RuntimeRegistryConfig points `erun version`'s image check at an internal mirror; unset uses ghcr.io/sophium/erun-devops.
type RuntimeRegistryConfig struct {
	Namespace  string `yaml:"namespace,omitempty"`
	Repository string `yaml:"repository,omitempty"`
	// BaseURL defaults to hub.docker.com or ghcr.io depending on the namespace.
	BaseURL string `yaml:"baseurl,omitempty"`
	// TokenURL is used only for GHCR; defaults to https://ghcr.io.
	TokenURL string `yaml:"tokenurl,omitempty"`
	// Insecure makes an unset BaseURL resolve to http://. Never persisted; the configured mirror is always HTTPS.
	Insecure bool `yaml:"-"`
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

var (
	ErrNotInitialized     = errors.New("not initialized")
	ErrNoUserDataFolder   = errors.New("failed to obtain config file locations")
	ErrConfigCorrupted    = errors.New("config file cannot be unmarshaled")
	ErrFailedToSaveConfig = errors.New("could not save struct to yaml file")
	ErrNotInGitRepository = errors.New("cannot find git project")
	// ErrUnusableStateName reports a name that cannot map to exactly one state directory.
	ErrUnusableStateName = errors.New("unusable tenant or environment name")
)
