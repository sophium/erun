package settings

import (
	"github.com/sophium/erun/erun-common/base/config"
)

type SSHDConfig struct {
	Enabled                    bool   `json:"enabled"`
	LocalPort                  int    `json:"localPort"`
	PublicKeyPath              string `json:"publicKeyPath"`
	WorkspaceSyncEnabled       bool   `json:"workspaceSyncEnabled"`
	WorkspaceSyncLocalPath     string `json:"workspaceSyncLocalPath,omitempty"`
	WorkspaceSyncStatus        string `json:"workspaceSyncStatus,omitempty"`
	WorkspaceSyncStatusMessage string `json:"workspaceSyncStatusMessage,omitempty"`
}

type EnvironmentLocalPorts struct {
	RangeStart          int        `json:"rangeStart"`
	RangeEnd            int        `json:"rangeEnd"`
	MCP                 int        `json:"mcp"`
	API                 int        `json:"api"`
	SSH                 int        `json:"ssh"`
	ContributeApp       int        `json:"contributeApp"`
	MCPStatus           PortStatus `json:"mcpStatus"`
	APIStatus           PortStatus `json:"apiStatus"`
	SSHStatus           PortStatus `json:"sshStatus"`
	ContributeAppStatus PortStatus `json:"contributeAppStatus"`
}

type PortStatus struct {
	Available bool   `json:"available"`
	Status    string `json:"status"`
}

// ContainerRegistryEntry mirrors config.ContainerRegistryEntry for the
// registry-list editor.
type ContainerRegistryEntry struct {
	Registry string `json:"registry"`
	// Cluster replaces Registry for a `cluster:` entry; its fields round-trip on save.
	Cluster *ContainerRegistryCluster `json:"cluster,omitempty"`
	Roles   []string                  `json:"roles"`
}

// ContainerRegistryCluster mirrors config.ClusterRegistry plus a display Label.
type ContainerRegistryCluster struct {
	Service   string `json:"service"`
	Namespace string `json:"namespace"`
	Port      int    `json:"port"`
	Insecure  bool   `json:"insecure"`
	Label     string `json:"label"`
}

type EnvironmentConfig struct {
	Name                string                   `json:"name"`
	Type                config.EnvironmentType   `json:"type,omitempty"`
	LocalRepoPath       string                   `json:"localRepoPath,omitempty"`
	RepoPath            string                   `json:"repoPath"`
	KubernetesContext   string                   `json:"kubernetesContext"`
	ContainerRegistries []ContainerRegistryEntry `json:"containerRegistries"`
	// ContainerRegistriesInherited: the list comes from the project's .erun/config.yaml.
	ContainerRegistriesInherited bool                    `json:"containerRegistriesInherited"`
	CloudProviderAlias           string                  `json:"cloudProviderAlias"`
	CloudProviderAliases         []string                `json:"cloudProviderAliases,omitempty"`
	CloudAliasSlots              []EnvironmentCloudAlias `json:"cloudAliasSlots,omitempty"`
	CloudContext                 *CloudContextStatus     `json:"cloudContext,omitempty"`
	RuntimeVersion               string                  `json:"runtimeVersion"`
	// RuntimeChart: empty means the chart published with the deployed version.
	RuntimeChart string `json:"runtimeChart,omitempty"`
	// RuntimeRegistry holds erun's own artifacts, distinct from the project's deploy registry.
	RuntimeRegistry  string           `json:"runtimeRegistry,omitempty"`
	ImagePullSecrets []string         `json:"imagePullSecrets,omitempty"`
	RuntimePod       RuntimePodConfig `json:"runtimePod"`
	SSHD             SSHDConfig       `json:"sshd"`
	Idle             IdleConfig       `json:"idle"`
	Claude           ClaudeConfig     `json:"claude"`
	ClaudeDefaults   ClaudeDefaults   `json:"claudeDefaults"`
	AITool           string           `json:"aiTool,omitempty"`
	// AILaunchSignature is read-only; a change after save reopens the AI tabs.
	AILaunchSignature     string                `json:"aiLaunchSignature"`
	LocalPorts            EnvironmentLocalPorts `json:"localPorts"`
	AutoStart             *bool                 `json:"autoStart,omitempty"`
	RemoteHostCredentials bool                  `json:"remoteHostCredentials"`
	AutoUpgrade           bool                  `json:"autoUpgrade"`
	UpgradeChannel        string                `json:"upgradeChannel,omitempty"`
	DisableBuildScript    bool                  `json:"disableBuildScript"`
	// PlatformAccount binds the runtime ServiceAccount to cluster-admin.
	PlatformAccount bool `json:"platformAccount"`
	// MountSource is a no-op without RepoURL.
	MountSource bool   `json:"mountSource"`
	RepoURL     string `json:"repoURL"`
	// DeployComponents: empty falls back to the repo plan, then the runtime chart alone.
	DeployComponents []string           `json:"deployComponents,omitempty"`
	Hosted           *HostedEnvironment `json:"hosted,omitempty"`
}

// HostedEnvironment is the desktop's view of config.HostedEnvironment.
type HostedEnvironment struct {
	Describe      string `json:"describe"`
	APIHost       string `json:"apiHost"`
	TenantID      string `json:"tenantId"`
	EnvironmentID string `json:"environmentId"`
	// DefinitionRevision zero means never synced.
	DefinitionRevision int `json:"definitionRevision"`
	// LocalChange needs no platform read, so it is always resolved.
	LocalChange HostedDefinitionLocalChange `json:"localChange"`
	// Drift is nil until compared, which does not mean "up to date".
	Drift *HostedDefinitionDrift `json:"drift,omitempty"`
}

// HostedDefinitionLocalChange mirrors config.HostedDefinitionLocalChange.
type HostedDefinitionLocalChange struct {
	// Available false (no recorded digest) must not render as "in step".
	Available bool   `json:"available"`
	Changed   bool   `json:"changed"`
	Describe  string `json:"describe"`
}

// HostedDefinitionDrift compares the platform revision with the last pulled one.
type HostedDefinitionDrift struct {
	LocalRevision    int    `json:"localRevision"`
	PlatformRevision int    `json:"platformRevision"`
	Behind           bool   `json:"behind"`
	Describe         string `json:"describe"`
	Error            string `json:"error,omitempty"`
}

type ClaudeConfig struct {
	UseMantle       *bool    `json:"useMantle,omitempty"`
	UseBedrock      *bool    `json:"useBedrock,omitempty"`
	UseGateway      *bool    `json:"useGateway,omitempty"`
	Models          []string `json:"models,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	Effort          *string  `json:"effort,omitempty"`
	DefaultModel    *string  `json:"defaultModel,omitempty"`
	VerboseDebug    bool     `json:"verboseDebug,omitempty"`
}

type ClaudeDefaults struct {
	UseMantle         bool     `json:"useMantle"`
	UseBedrock        bool     `json:"useBedrock"`
	Models            []string `json:"models"`
	MaxOutputTokens   int      `json:"maxOutputTokens"`
	KnownModels       []string `json:"knownModels"`
	MinTokens         int      `json:"minTokens"`
	MaxTokens         int      `json:"maxTokens"`
	Effort            string   `json:"effort"`
	EffortLevels      []string `json:"effortLevels"`
	GatewayConfigured bool     `json:"gatewayConfigured"`
	// Gateway models sit beside Models; the draft's useGateway picks which set applies.
	GatewayModels       []string `json:"gatewayModels"`
	GatewayDefaultModel string   `json:"gatewayDefaultModel"`
}

type RuntimePodConfig struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

type IdleConfig struct {
	Timeout          string `json:"timeout"`
	WorkingHours     string `json:"workingHours"`
	IdleTrafficBytes int64  `json:"idleTrafficBytes"`
}
