package main

import (
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/deploy"
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

// uiContainerRegistryEntry mirrors eruncommon.ContainerRegistryEntry for the
// desktop registry-list editor. Roles carry the value set build/from/to/deploy
// but ride as plain strings because RegistryRole is a string alias at the Wails
// boundary.
type uiContainerRegistryEntry struct {
	Registry string `json:"registry"`
	// Cluster is set for a context-resolved in-cluster registry (a `cluster:`
	// entry) instead of a static Registry host. It carries a legible Label so the
	// editor renders it as a readable line rather than a blank text box, and its
	// concrete fields so the entry round-trips losslessly on save.
	Cluster *uiContainerRegistryCluster `json:"cluster,omitempty"`
	Roles   []string                    `json:"roles"`
}

// uiContainerRegistryCluster mirrors eruncommon.ClusterRegistry for the desktop
// registry-list editor, plus a Label the frontend shows verbatim.
type uiContainerRegistryCluster struct {
	Service   string `json:"service"`
	Namespace string `json:"namespace"`
	Port      int    `json:"port"`
	Insecure  bool   `json:"insecure"`
	Label     string `json:"label"`
}

// uiDeployComponents is the Runtime tab's read model for a deploy at a chosen
// version: the charts it would roll out, and which chart the runtime itself would
// be installed from -- the second coordinate the version does not name.
type uiDeployComponents struct {
	Components   []deploy.DeployableComponent `json:"components"`
	RuntimeChart uiRuntimeChartPlan           `json:"runtimeChart"`
}

type uiEnvironmentConfig struct {
	Name                string                     `json:"name"`
	Type                config.EnvironmentType     `json:"type,omitempty"`
	LocalRepoPath       string                     `json:"localRepoPath,omitempty"`
	RepoPath            string                     `json:"repoPath"`
	KubernetesContext   string                     `json:"kubernetesContext"`
	ContainerRegistries []uiContainerRegistryEntry `json:"containerRegistries"`
	// ContainerRegistriesInherited is true when the shown list is resolved from
	// the project's .erun/config.yaml (a local-agent env with no env-level
	// override) rather than carried on the env config. The editor marks it so the
	// operator sees these are inherited-from-project, not an env override.
	ContainerRegistriesInherited bool                      `json:"containerRegistriesInherited"`
	CloudProviderAlias           string                    `json:"cloudProviderAlias"`
	CloudProviderAliases         []string                  `json:"cloudProviderAliases,omitempty"`
	CloudAliasSlots              []uiEnvironmentCloudAlias `json:"cloudAliasSlots,omitempty"`
	CloudContext                 *uiCloudContextStatus     `json:"cloudContext,omitempty"`
	RuntimeVersion               string                    `json:"runtimeVersion"`
	// RuntimeChart is the chart this env's runtime is installed from, stated as an
	// OCI reference that may carry its own version. Empty means "the chart
	// published with the deployed version", which is right whenever the chart and
	// the runtime image ride one release line. See EnvConfig.RuntimeChart.
	RuntimeChart string `json:"runtimeChart,omitempty"`
	// RuntimeRegistry is where the env resolves erun's OWN artifacts from -- the
	// runtime chart and platform images -- as distinct from the registry this
	// project's images are pushed to. Set it when the deploy registry holds only
	// the project's images, so erun's chart is not there to pull.
	RuntimeRegistry string `json:"runtimeRegistry,omitempty"`
	// ImagePullSecrets names the Kubernetes dockerconfigjson secrets the runtime
	// pod pulls its image with. Without one a private runtime image leaves the
	// pod in ImagePullBackOff, which the app could previously cause and not fix.
	ImagePullSecrets []string           `json:"imagePullSecrets,omitempty"`
	RuntimePod       uiRuntimePodConfig `json:"runtimePod"`
	SSHD             uiSSHDConfig       `json:"sshd"`
	Idle             uiIdleConfig       `json:"idle"`
	Claude           uiClaudeConfig     `json:"claude"`
	ClaudeDefaults   uiClaudeDefaults   `json:"claudeDefaults"`
	AITool           string             `json:"aiTool,omitempty"`
	// AILaunchSignature is the command the environment's AI tab launches with
	// for the persisted config, computed by the same eruncommon resolution the
	// launch uses (gateway, model, effort, Remote Control). A save whose value
	// changed reopens the open AI tabs; deciding that here rather than from a
	// copy of the rules in the frontend keeps the two from disagreeing. Read
	// only: a save ignores it and returns a fresh one.
	AILaunchSignature     string                  `json:"aiLaunchSignature"`
	LocalPorts            uiEnvironmentLocalPorts `json:"localPorts"`
	AutoStart             *bool                   `json:"autoStart,omitempty"`
	RemoteHostCredentials bool                    `json:"remoteHostCredentials"`
	AutoUpgrade           bool                    `json:"autoUpgrade"`
	UpgradeChannel        string                  `json:"upgradeChannel,omitempty"`
	DisableBuildScript    bool                    `json:"disableBuildScript"`
	// PlatformAccount binds the env's runtime ServiceAccount to cluster-admin so
	// in-pod platform Terraform (the cluster edge) and component installs can
	// manage cluster-scoped resources. See EnvConfig.PlatformAccount.
	PlatformAccount bool `json:"platformAccount"`
	// MountSource opts a runtime env into a mutable source worktree the pod clones
	// at the deployed release ref; RepoURL is the git remote it clones. Runtime
	// envs only, and a no-op without RepoURL. See EnvConfig.MountsRuntimeSource.
	MountSource bool   `json:"mountSource"`
	RepoURL     string `json:"repoURL"`
	// DeployComponents is the per-machine saved deploy selection: the charts
	// `erun deploy` rolls out for this env by default. Empty means no saved
	// selection — deploy falls back to the repo plan, then the runtime chart
	// alone. Editing it raises the pending-redeploy banner, since it changes what
	// a redeploy rolls out.
	DeployComponents []string `json:"deployComponents,omitempty"`
	// Hosted is the platform row this environment corresponds to, when it has
	// one. Nil means the environment is not marked as hosted. See
	// eruncommon.HostedEnvironment.
	Hosted *uiHostedEnvironment `json:"hosted,omitempty"`
}

// uiHostedEnvironment is the desktop's view of eruncommon.HostedEnvironment:
// which platform row this local environment corresponds to, and how far the
// local copy has fallen behind the definition that row holds.
type uiHostedEnvironment struct {
	// Describe renders the row for the operator: host, tenant, environment.
	Describe string `json:"describe"`
	APIHost  string `json:"apiHost"`
	TenantID string `json:"tenantId"`
	// EnvironmentID is the platform row's environment_id, shown so the
	// operator can match the local environment against `erun platform env
	// list` without guessing.
	EnvironmentID string `json:"environmentId"`
	// DefinitionRevision is the revision this local copy was last synced
	// from; zero means it has never been synced.
	DefinitionRevision int `json:"definitionRevision"`
	// LocalChange is whether this machine's settings have moved since that
	// revision was recorded. Unlike Drift it needs no platform read, so it is
	// always resolved — including for an operator who has never connected.
	LocalChange uiHostedDefinitionLocalChange `json:"localChange"`
	// Drift is the read-only "the platform has moved" hint, resolved on the
	// desktop's own poll. Nil when no comparison has been made yet, which
	// reads as "nothing to say" rather than as "up to date".
	Drift *uiHostedDefinitionDrift `json:"drift,omitempty"`
}

// uiHostedDefinitionLocalChange mirrors eruncommon.HostedDefinitionLocalChange:
// whether this machine's portable settings have moved since the marker last
// recorded a transfer. It is the offline half of the marker panel — no
// platform read, no button — which is what makes a change made while the
// desktop was closed visible at all.
type uiHostedDefinitionLocalChange struct {
	// Available is false when the marker records no digest, so nothing can be
	// said either way. It is deliberately not rendered as "in step".
	Available bool   `json:"available"`
	Changed   bool   `json:"changed"`
	Describe  string `json:"describe"`
}

// uiHostedDefinitionDrift is the half of the marker panel that only the
// platform can answer: what it holds against what this machine last pulled.
type uiHostedDefinitionDrift struct {
	LocalRevision    int  `json:"localRevision"`
	PlatformRevision int  `json:"platformRevision"`
	Behind           bool `json:"behind"`
	// Describe is the operator-facing sentence, worded once in
	// eruncommon.HostedDefinitionDrift.Describe so the CLI and the desktop
	// cannot phrase the same state two ways.
	Describe string `json:"describe"`
	// Error is set when the comparison could not be made; it states what could
	// not be read before the cause, per the Diagnostic Decision Record in
	// erun-common/AGENTS.md.
	Error string `json:"error,omitempty"`
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
	UseMantle       bool     `json:"useMantle"`
	UseBedrock      bool     `json:"useBedrock"`
	Models          []string `json:"models"`
	MaxOutputTokens int      `json:"maxOutputTokens"`
	KnownModels     []string `json:"knownModels"`
	MinTokens       int      `json:"minTokens"`
	MaxTokens       int      `json:"maxTokens"`
	Effort          string   `json:"effort"`
	EffortLevels    []string `json:"effortLevels"`
	// GatewayConfigured reports whether the erun-level catalog names a gateway.
	// Without one the per-environment controls have nothing to override, so the
	// AI tab hides them rather than offering a switch that changes nothing.
	GatewayConfigured bool `json:"gatewayConfigured"`
	// GatewayModels and GatewayDefaultModel are the catalog's selectable models
	// and the model a gateway launch falls back to. They stand beside Models and
	// KnownModels rather than replacing them, because which set applies is the
	// environment's own choice: the AI tab picks from the draft's useGateway, so
	// an environment that leaves the gateway is offered its own models at once.
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
