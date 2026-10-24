package main

import (
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/deploy/components"
)

type uiSSHDConfig struct {
	Enabled                    bool   `json:"enabled"`
	LocalPort                  int    `json:"localPort"`
	PublicKeyPath              string `json:"publicKeyPath"`
	WorkspaceSyncEnabled       bool   `json:"workspaceSyncEnabled"`
	WorkspaceSyncLocalPath     string `json:"workspaceSyncLocalPath,omitempty"`
	WorkspaceSyncStatus        string `json:"workspaceSyncStatus,omitempty"`
	WorkspaceSyncStatusMessage string `json:"workspaceSyncStatusMessage,omitempty"`
}

type uiEnvironmentLocalPorts struct {
	RangeStart          int          `json:"rangeStart"`
	RangeEnd            int          `json:"rangeEnd"`
	MCP                 int          `json:"mcp"`
	API                 int          `json:"api"`
	SSH                 int          `json:"ssh"`
	ContributeApp       int          `json:"contributeApp"`
	MCPStatus           uiPortStatus `json:"mcpStatus"`
	APIStatus           uiPortStatus `json:"apiStatus"`
	SSHStatus           uiPortStatus `json:"sshStatus"`
	ContributeAppStatus uiPortStatus `json:"contributeAppStatus"`
}

type uiPortStatus struct {
	Available bool   `json:"available"`
	Status    string `json:"status"`
}

// uiContainerRegistryEntry mirrors config.ContainerRegistryEntry for the
// registry-list editor.
type uiContainerRegistryEntry struct {
	Registry string `json:"registry"`
	// Cluster replaces Registry for a `cluster:` entry; its fields round-trip on save.
	Cluster *uiContainerRegistryCluster `json:"cluster,omitempty"`
	Roles   []string                    `json:"roles"`
}

// uiContainerRegistryCluster mirrors config.ClusterRegistry plus a display Label.
type uiContainerRegistryCluster struct {
	Service   string `json:"service"`
	Namespace string `json:"namespace"`
	Port      int    `json:"port"`
	Insecure  bool   `json:"insecure"`
	Label     string `json:"label"`
}

// uiDeployComponents is what a deploy at a chosen version would roll out.
type uiDeployComponents struct {
	Components   []components.DeployableComponent `json:"components"`
	RuntimeChart uiRuntimeChartPlan               `json:"runtimeChart"`
}

type uiEnvironmentConfig struct {
	Name                string                     `json:"name"`
	Type                config.EnvironmentType     `json:"type,omitempty"`
	LocalRepoPath       string                     `json:"localRepoPath,omitempty"`
	RepoPath            string                     `json:"repoPath"`
	KubernetesContext   string                     `json:"kubernetesContext"`
	ContainerRegistries []uiContainerRegistryEntry `json:"containerRegistries"`
	// ContainerRegistriesInherited: the list comes from the project's .erun/config.yaml.
	ContainerRegistriesInherited bool                      `json:"containerRegistriesInherited"`
	CloudProviderAlias           string                    `json:"cloudProviderAlias"`
	CloudProviderAliases         []string                  `json:"cloudProviderAliases,omitempty"`
	CloudAliasSlots              []uiEnvironmentCloudAlias `json:"cloudAliasSlots,omitempty"`
	CloudContext                 *uiCloudContextStatus     `json:"cloudContext,omitempty"`
	RuntimeVersion               string                    `json:"runtimeVersion"`
	// RuntimeChart: empty means the chart published with the deployed version.
	RuntimeChart string `json:"runtimeChart,omitempty"`
	// RuntimeRegistry holds erun's own artifacts, distinct from the project's deploy registry.
	RuntimeRegistry  string             `json:"runtimeRegistry,omitempty"`
	ImagePullSecrets []string           `json:"imagePullSecrets,omitempty"`
	RuntimePod       uiRuntimePodConfig `json:"runtimePod"`
	SSHD             uiSSHDConfig       `json:"sshd"`
	Idle             uiIdleConfig       `json:"idle"`
	Claude           uiClaudeConfig     `json:"claude"`
	ClaudeDefaults   uiClaudeDefaults   `json:"claudeDefaults"`
	AITool           string             `json:"aiTool,omitempty"`
	// AILaunchSignature is read-only; a change after save reopens the AI tabs.
	AILaunchSignature     string                  `json:"aiLaunchSignature"`
	LocalPorts            uiEnvironmentLocalPorts `json:"localPorts"`
	AutoStart             *bool                   `json:"autoStart,omitempty"`
	RemoteHostCredentials bool                    `json:"remoteHostCredentials"`
	AutoUpgrade           bool                    `json:"autoUpgrade"`
	UpgradeChannel        string                  `json:"upgradeChannel,omitempty"`
	DisableBuildScript    bool                    `json:"disableBuildScript"`
	// PlatformAccount binds the runtime ServiceAccount to cluster-admin.
	PlatformAccount bool `json:"platformAccount"`
	// MountSource is a no-op without RepoURL.
	MountSource bool   `json:"mountSource"`
	RepoURL     string `json:"repoURL"`
	// DeployComponents: empty falls back to the repo plan, then the runtime chart alone.
	DeployComponents []string             `json:"deployComponents,omitempty"`
	Hosted           *uiHostedEnvironment `json:"hosted,omitempty"`
}

// uiHostedEnvironment is the desktop's view of config.HostedEnvironment.
type uiHostedEnvironment struct {
	Describe      string `json:"describe"`
	APIHost       string `json:"apiHost"`
	TenantID      string `json:"tenantId"`
	EnvironmentID string `json:"environmentId"`
	// DefinitionRevision zero means never synced.
	DefinitionRevision int `json:"definitionRevision"`
	// LocalChange needs no platform read, so it is always resolved.
	LocalChange uiHostedDefinitionLocalChange `json:"localChange"`
	// Drift is nil until compared, which does not mean "up to date".
	Drift *uiHostedDefinitionDrift `json:"drift,omitempty"`
}

// uiHostedDefinitionLocalChange mirrors config.HostedDefinitionLocalChange.
type uiHostedDefinitionLocalChange struct {
	// Available false (no recorded digest) must not render as "in step".
	Available bool   `json:"available"`
	Changed   bool   `json:"changed"`
	Describe  string `json:"describe"`
}

// uiHostedDefinitionDrift compares the platform revision with the last pulled one.
type uiHostedDefinitionDrift struct {
	LocalRevision    int    `json:"localRevision"`
	PlatformRevision int    `json:"platformRevision"`
	Behind           bool   `json:"behind"`
	Describe         string `json:"describe"`
	Error            string `json:"error,omitempty"`
}

type uiClaudeConfig struct {
	UseMantle       *bool    `json:"useMantle,omitempty"`
	UseBedrock      *bool    `json:"useBedrock,omitempty"`
	UseGateway      *bool    `json:"useGateway,omitempty"`
	Models          []string `json:"models,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	Effort          *string  `json:"effort,omitempty"`
	DefaultModel    *string  `json:"defaultModel,omitempty"`
	VerboseDebug    bool     `json:"verboseDebug,omitempty"`
}

type uiClaudeDefaults struct {
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

type uiRuntimePodConfig struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

type uiIdleConfig struct {
	Timeout          string `json:"timeout"`
	WorkingHours     string `json:"workingHours"`
	IdleTrafficBytes int64  `json:"idleTrafficBytes"`
}
