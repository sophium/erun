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
	// Orchestrators are the operator's persisted host-side AI orchestrator
	// definitions (see OrchestratorConfig). They live in root config so the same
	// set reappears across desktop restarts; the running session itself is
	// ephemeral and re-spawned on demand.
	Orchestrators []OrchestratorConfig `yaml:"orchestrators,omitempty" json:"orchestrators,omitempty"`
	// Whip overrides the pacing nudge's message/threshold/cap/automatic-pass
	// defaults (see WhipConfig, ResolveWhipConfig). Nil keeps every unconfigured
	// install on exactly today's behaviour.
	Whip *WhipConfigOverride `yaml:"whip,omitempty" json:"whip,omitempty"`
	// Execution holds per-operation subprocess/library execution mode
	// overrides (see execution_mode.go). Absent keeps every operation on the
	// subprocess path it has always used.
	Execution ExecutionConfig `yaml:"execution,omitempty" json:"execution,omitempty"`
	// OpenRouter holds the operator's erun-level gateway catalog (see
	// openrouter.go): the gateway an environment's Claude Code is routed
	// through, and the models an environment may select from it. Nil keeps an
	// install that has not configured a gateway on exactly today's behaviour.
	OpenRouter *OpenRouterConfig `yaml:"openrouter,omitempty" json:"openRouter,omitempty"`
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
	// Insecure marks Namespace as plain HTTP (a cluster registry marked
	// `insecure: true`), so an unset BaseURL resolves to http:// instead of
	// https:// for it. Never persisted -- only a caller that already knows the
	// registry is insecure (e.g. probing a tenant's own published chart) sets
	// it; the operator-configured mirror this struct otherwise describes is
	// always addressed over HTTPS.
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
	// ErrUnusableStateName reports a tenant or environment that cannot name
	// exactly one directory in the state tree. See validateStatePathSegment.
	ErrUnusableStateName = errors.New("unusable tenant or environment name")
)
