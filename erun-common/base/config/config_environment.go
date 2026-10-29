package config

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
)

// EnvironmentType classifies an environment by where its worktree lives and whether it builds.
type EnvironmentType string

const (
	EnvironmentTypeLocalAgent  EnvironmentType = "local-agent"
	EnvironmentTypeRemoteAgent EnvironmentType = "remote-agent"
	EnvironmentTypeRuntime     EnvironmentType = "runtime"
	// EnvironmentTypeHost is a directory on the operator's machine with no pod or cluster.
	EnvironmentTypeHost EnvironmentType = "host"
)

// ValidEnvironmentTypes lists every type; a completeness test walks it so new types need explicit cases.
var ValidEnvironmentTypes = []EnvironmentType{
	EnvironmentTypeLocalAgent,
	EnvironmentTypeRemoteAgent,
	EnvironmentTypeRuntime,
	EnvironmentTypeHost,
}

// IsValid reports whether t is a canonical type; empty is not valid.
func (t EnvironmentType) IsValid() bool {
	for _, valid := range ValidEnvironmentTypes {
		if t == valid {
			return true
		}
	}
	return false
}

// UsesDindSidecar reports whether this type runs the erun-dind build sidecar. Mirrors the
// chart's $dindEnabled and environmentUsesDindSidecar in Sidebar.helpers.ts; keep in sync.
func (t EnvironmentType) UsesDindSidecar() bool {
	if !t.IsValid() {
		return false
	}
	switch t {
	case EnvironmentTypeLocalAgent, EnvironmentTypeRemoteAgent:
		return true
	case EnvironmentTypeRuntime, EnvironmentTypeHost:
		return false
	default:
		panic(fmt.Sprintf("UsesDindSidecar: unhandled EnvironmentType %q", t))
	}
}

type EnvConfig struct {
	Name          string
	Type          EnvironmentType `yaml:"type,omitempty" json:"type,omitempty"`
	LocalRepoPath string          `yaml:"localrepopath,omitempty" json:"localRepoPath,omitempty"`
	// MountSource makes a runtime pod clone RepoURL at the release ref into a PVC worktree.
	// Requires RepoURL; ignored for agent envs.
	MountSource        bool   `yaml:"mountsource,omitempty" json:"mountSource,omitempty"`
	RepoURL            string `yaml:"repourl,omitempty" json:"repoURL,omitempty"`
	KubernetesContext  string
	CloudProviderAlias string `yaml:"cloudprovideralias,omitempty"`
	// CloudProviderAliases holds at most one alias per provider type; legacy CloudProviderAlias is the AWS slot.
	CloudProviderAliases map[string]string `yaml:"cloudprovideraliases,omitempty" json:"cloudProviderAliases,omitempty"`
	ManagedCloud         bool              `yaml:"managedcloud,omitempty" json:"managedCloud,omitempty"`
	// Hosted names the platform row this env maps to; a local assertion, separate from ManagedCloud.
	Hosted          HostedEnvironment `yaml:"hosted,omitempty" json:"hosted,omitempty"`
	RuntimeVersion  string            `yaml:"runtimeversion,omitempty"`
	RuntimeRegistry string            `yaml:"runtimeregistry,omitempty" json:"runtimeRegistry,omitempty"`
	// ContainerRegistries holds the registry list for envs whose project config is not local.
	ContainerRegistries ContainerRegistries `yaml:"containerregistries,omitempty" json:"containerRegistries,omitempty"`
	// RuntimeImage overrides the runtime pod image; a bare name resolves against the env's registry and version.
	RuntimeImage string `yaml:"runtimeimage,omitempty" json:"runtimeImage,omitempty"`
	// RuntimeRunningImage is a display-only memo of the image the last deploy resolved. It never
	// drives a deploy; empty means undetermined, and callers must not guess a line.
	RuntimeRunningImage string `yaml:"runtimerunningimage,omitempty" json:"runtimeRunningImage,omitempty"`
	// RuntimeChart pins the runtime chart OCI reference, for envs whose image rides a different
	// release line than erun's chart. Empty uses the published chart at the runtime version.
	RuntimeChart string `yaml:"runtimechart,omitempty" json:"runtimeChart,omitempty"`
	// MCPAuthPublicKeyPath is rethreaded on redeploy so MCP auth is not silently dropped.
	MCPAuthPublicKeyPath string `yaml:"mcpauthpublickeypath,omitempty" json:"mcpAuthPublicKeyPath,omitempty"`
	// ImagePullSecrets names operator-provisioned dockerconfigjson secrets for private images.
	ImagePullSecrets []string `yaml:"imagepullsecrets,omitempty" json:"imagePullSecrets,omitempty"`
	// RegistryCredentialSecretName is the registry Secret `erun init` minted from the host credential.
	// Only init mints or rotates it.
	RegistryCredentialSecretName string `yaml:"registrycredentialsecretname,omitempty" json:"registryCredentialSecretName,omitempty"`
	// PlatformAliasSecretName is the Secret `erun init` minted from the host's platform alias.
	// Only init mints or rotates it.
	PlatformAliasSecretName string                    `yaml:"platformaliassecretname,omitempty" json:"platformAliasSecretName,omitempty"`
	RuntimePod              model.RuntimePodResources `yaml:"runtimepod,omitempty"`
	// RuntimeDindPod sizes the dind sidecar; zero uses the defaults.
	RuntimeDindPod model.RuntimePodResources `yaml:"runtimedindpod,omitempty"`
	// NamespaceQuota is a hard per-namespace ceiling, distinct from RuntimePod sizing.
	NamespaceQuota      model.NamespaceResourceQuota `yaml:"namespacequota,omitempty" json:"namespaceQuota,omitempty"`
	SSHD                SSHDConfig                   `yaml:"sshd,omitempty"`
	Idle                EnvironmentIdleConfig        `yaml:"idle,omitempty"`
	Deploy              EnvironmentDeployConfig      `yaml:"deploy,omitempty" json:"deploy,omitempty"`
	Claude              EnvironmentClaudeConfig      `yaml:"claude,omitempty" json:"claude,omitempty"`
	AITool              string                       `yaml:"aitool,omitempty" json:"aiTool,omitempty"`
	LocalPortRangeStart int                          `yaml:"localportrangestart,omitempty" json:"localPortRangeStart,omitempty"`
	AutoStart           *bool                        `yaml:"autostart,omitempty" json:"autoStart,omitempty"`
	// Deprecated: no effect; AWS credential delivery follows HasAWSCloudAlias.
	RemoteHostCredentials bool `yaml:"remotehostcredentials,omitempty" json:"remoteHostCredentials,omitempty"`
	// AutoUpgrade opts this env into "Upgrade all".
	AutoUpgrade bool `yaml:"autoupgrade,omitempty" json:"autoUpgrade,omitempty"`
	// UpgradeChannel is "stable" or "snapshot"; empty resolves from Type.
	UpgradeChannel string `yaml:"upgradechannel,omitempty" json:"upgradeChannel,omitempty"`
	// DisableBuildScript ignores the project's build.sh for this env.
	DisableBuildScript bool `yaml:"disablebuildscript,omitempty" json:"disableBuildScript,omitempty"`
	// PlatformAccount binds the runtime ServiceAccount to cluster-admin for cluster-scoped installs.
	PlatformAccount bool `yaml:"platformaccount,omitempty" json:"platformAccount,omitempty"`
	// Stopped keeps the runtime scaled to zero across helm upgrades; `erun open` clears it.
	Stopped bool `yaml:"stopped,omitempty" json:"stopped,omitempty"`
}

// ResolvedCloudAliases returns aliases by provider type, folding in the legacy AWS scalar.
func (c EnvConfig) ResolvedCloudAliases() map[string]string {
	resolved := make(map[string]string)
	if alias := strings.TrimSpace(c.CloudProviderAlias); alias != "" {
		resolved[CloudProviderTypeFromAlias(alias)] = alias
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

// ResolvedType returns the env's type, or "" when unresolved.
func (c EnvConfig) ResolvedType() EnvironmentType {
	if c.Type.IsValid() {
		return c.Type
	}
	return ""
}

// BuildsHere reports whether builds happen inside this env; unknown types panic.
func (c EnvConfig) BuildsHere() bool {
	if !c.Type.IsValid() {
		return false
	}
	switch c.Type {
	case EnvironmentTypeLocalAgent, EnvironmentTypeRemoteAgent, EnvironmentTypeHost:
		return true
	case EnvironmentTypeRuntime:
		return false
	default:
		panic(fmt.Sprintf("BuildsHere: unhandled EnvironmentType %q", c.Type))
	}
}

// RemoteWorktree reports whether the worktree lives off this machine; unknown types panic.
func (c EnvConfig) RemoteWorktree() bool {
	if !c.Type.IsValid() {
		return false
	}
	switch c.Type {
	case EnvironmentTypeRemoteAgent, EnvironmentTypeRuntime:
		return true
	case EnvironmentTypeLocalAgent, EnvironmentTypeHost:
		return false
	default:
		panic(fmt.Sprintf("RemoteWorktree: unhandled EnvironmentType %q", c.Type))
	}
}

// HasPod reports whether this env has a runtime pod; pod-only operations must refuse when false.
func (c EnvConfig) HasPod() bool {
	return c.Type.IsValid() && c.Type != EnvironmentTypeHost
}

// MountsRuntimeSource reports whether a runtime env has both MountSource and RepoURL.
func (c EnvConfig) MountsRuntimeSource() bool {
	return c.Type == EnvironmentTypeRuntime && c.MountSource && strings.TrimSpace(c.RepoURL) != ""
}

// HasAWSCloudAlias reports whether an AWS alias is attached, which alone drives AWS credential delivery.
func (c EnvConfig) HasAWSCloudAlias() bool {
	return strings.TrimSpace(c.ResolvedCloudAliases()[CloudProviderAWS]) != ""
}

// RuntimeImageRegistryMismatch reports a RuntimeImage registry that differs from RuntimeRegistry,
// which `erun doctor` flags; false when either side names no registry.
func (c EnvConfig) RuntimeImageRegistryMismatch() (imageRegistry, runtimeRegistry string, mismatched bool) {
	imageRegistry = RuntimeImageRegistry(c.RuntimeImage)
	runtimeRegistry = strings.TrimSpace(c.RuntimeRegistry)
	if imageRegistry == "" || runtimeRegistry == "" {
		return imageRegistry, runtimeRegistry, false
	}
	return imageRegistry, runtimeRegistry, !strings.EqualFold(imageRegistry, runtimeRegistry)
}

// RuntimeImageLineMismatch reports whether RuntimeImage and RuntimeRunningImage name different
// release lines; false when either is empty or unparseable.
func (c EnvConfig) RuntimeImageLineMismatch() (recordedLine, observedLine string, mismatched bool) {
	recordedLine, recordedOK := RuntimeImageReleaseLine(c.RuntimeImage)
	observedLine, observedOK := RuntimeImageReleaseLine(c.RuntimeRunningImage)
	if !recordedOK || !observedOK {
		return recordedLine, observedLine, false
	}
	return recordedLine, observedLine, recordedLine != observedLine
}

// LegacyEnvTypeFromRemoteSnapshot maps the pre-`type` remote+snapshot pair to a type, matching the old deciders exactly.
func LegacyEnvTypeFromRemoteSnapshot(remote bool, snapshot *bool) EnvironmentType {
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

// IsValidUpgradeChannel reports whether channel is a canonical channel.
func IsValidUpgradeChannel(channel string) bool {
	switch strings.TrimSpace(channel) {
	case UpgradeChannelStable, UpgradeChannelSnapshot:
		return true
	}
	return false
}

// ResolvedUpgradeChannel returns UpgradeChannel when valid, else "stable" for runtime and
// unresolved envs and "snapshot" for agent and host envs.
func (c EnvConfig) ResolvedUpgradeChannel() string {
	if IsValidUpgradeChannel(c.UpgradeChannel) {
		return strings.TrimSpace(c.UpgradeChannel)
	}
	switch c.ResolvedType() {
	case EnvironmentTypeLocalAgent, EnvironmentTypeRemoteAgent, EnvironmentTypeHost:
		return UpgradeChannelSnapshot
	case EnvironmentTypeRuntime:
		return UpgradeChannelStable
	default:
		return UpgradeChannelStable
	}
}

// EffectiveLocalRepoPath returns the host repo path, including the legacy `repopath` key.
func (c EnvConfig) EffectiveLocalRepoPath() string {
	return strings.TrimSpace(c.LocalRepoPath)
}
