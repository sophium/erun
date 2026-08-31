package eruncommon

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
}

// OrchestratorConfig is a persisted host-side AI orchestrator definition. An
// orchestrator drives one or more agent environments from the operator's machine,
// reviewing each in a host directory read-only.
type OrchestratorConfig struct {
	ID           string                  `yaml:"id" json:"id"`
	Name         string                  `yaml:"name" json:"name"`
	Environments []OrchestratorEnvConfig `yaml:"environments,omitempty" json:"environments,omitempty"`
}

// OrchestratorEnvConfig links one agent environment to the orchestrator's
// read-only review window on the host: the directory a remote-agent env's
// workspace sync mirrors into, or a local-agent env's own worktree, which is
// already on this machine because its pod hostPath-mounts it.
type OrchestratorEnvConfig struct {
	Tenant      string `yaml:"tenant" json:"tenant"`
	Environment string `yaml:"environment" json:"environment"`
	Directory   string `yaml:"directory" json:"directory"`
	// Role states what this orchestrator uses the environment for. It is
	// orthogonal to EnvironmentType: type is where the worktree lives, role is
	// what this orchestrator asks the environment to do, and several
	// orchestrators can link the same environment with different roles. Empty
	// means undeclared, never a default of either OrchestratorEnvRoleCode,
	// OrchestratorEnvRoleBuild, or OrchestratorEnvRoleRuntime. The two are
	// independent even where they share a spelling: a runtime-*type*
	// environment linked with role "code" is still refused (see
	// OrchestratorEnvRoleAllowed) -- nothing here derives one from the other.
	Role OrchestratorEnvRole `yaml:"role,omitempty" json:"role,omitempty"`
}

// OrchestratorEnvRole is what an orchestrator uses a linked environment for.
type OrchestratorEnvRole string

const (
	// OrchestratorEnvRoleCode is an environment that writes code, iterates
	// fast, and pushes feature branches. It does not run full regressions.
	OrchestratorEnvRoleCode OrchestratorEnvRole = "code"
	// OrchestratorEnvRoleBuild is an environment that checks out pushed
	// feature branches, runs the gates, fixes what the gates surface, and cuts
	// releases.
	OrchestratorEnvRoleBuild OrchestratorEnvRole = "build"
	// OrchestratorEnvRoleRuntime is an environment this orchestrator
	// operates -- deploy, pin, observe -- rather than reviews or delegates
	// to. A runtime environment has no worktree and no in-pod agent, so this
	// is the only role that may be declared for one; see
	// OrchestratorEnvRoleAllowed.
	OrchestratorEnvRoleRuntime OrchestratorEnvRole = "runtime"
)

// IsValid reports whether r is either undeclared (empty) or a known role.
func (r OrchestratorEnvRole) IsValid() bool {
	switch r {
	case "", OrchestratorEnvRoleCode, OrchestratorEnvRoleBuild, OrchestratorEnvRoleRuntime:
		return true
	default:
		return false
	}
}

// OrchestratorEnvRoleAllowed reports whether role may be declared for a link
// to an environment of envType -- the single decision the CLI's
// SetOrchestratorEnvRole and the desktop's link/edit gate both consult, so
// neither can drift from the other on what a role is allowed to be. A
// local-agent, remote-agent, or host environment has a worktree to review and
// an in-pod agent to delegate to, so any role -- including undeclared -- is
// fine. A runtime environment has neither, so only OrchestratorEnvRoleRuntime
// may be declared for it; code and build, which both presuppose what it
// lacks, are refused, the same as any type this function does not recognize.
func OrchestratorEnvRoleAllowed(envType EnvironmentType, role OrchestratorEnvRole) bool {
	switch envType {
	case EnvironmentTypeLocalAgent, EnvironmentTypeRemoteAgent, EnvironmentTypeHost:
		return true
	case EnvironmentTypeRuntime:
		return role == OrchestratorEnvRoleRuntime
	default:
		return false
	}
}

// OrchestratorEnvRoleRequiredFor returns the one role a link to an
// environment of envType must declare for OrchestratorEnvRoleAllowed to
// accept it, or "" when every role -- including undeclared -- already works.
// Only a runtime environment constrains this today.
func OrchestratorEnvRoleRequiredFor(envType EnvironmentType) OrchestratorEnvRole {
	if envType == EnvironmentTypeRuntime {
		return OrchestratorEnvRoleRuntime
	}
	return ""
}

// OrchestratorEnvRoleIneligibilityReason explains, in the operator's words,
// why role may not be declared for envType -- the shared reason both the
// CLI's SetOrchestratorEnvRole and the desktop's link/edit gate surface, so
// an operator sees the same explanation regardless of which surface refused
// them. Empty when OrchestratorEnvRoleAllowed(envType, role) is true.
func OrchestratorEnvRoleIneligibilityReason(envType EnvironmentType, role OrchestratorEnvRole) string {
	if OrchestratorEnvRoleAllowed(envType, role) {
		return ""
	}
	if envType == EnvironmentTypeRuntime {
		return "Runtime environments have no worktree to review and no in-pod agent to delegate to, " +
			"so they can't be linked to an orchestrator with the code or build role. Link with the " +
			"runtime role instead to operate it directly."
	}
	return "This environment's type isn't recognized, so it can't be linked to an orchestrator."
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

// EnvironmentType classifies an environment by where its worktree lives and
// whether builds happen inside it. See docs at /concepts/environment-types.
type EnvironmentType string

const (
	EnvironmentTypeLocalAgent  EnvironmentType = "local-agent"
	EnvironmentTypeRemoteAgent EnvironmentType = "remote-agent"
	EnvironmentTypeRuntime     EnvironmentType = "runtime"
	// EnvironmentTypeHost is a worktree on the operator's own machine with no
	// pod and no cluster at all — for work a pod cannot do (desktop app builds
	// needing a GUI toolchain, tasks needing host-wide credentials such as a
	// keychain or a code-signing identity). Unlike EnvironmentTypeLocalAgent,
	// which is the same kind of host directory but hostPath-mounted into a pod
	// that runs its agent, a host env has no pod to mount it into.
	EnvironmentTypeHost EnvironmentType = "host"
)

// validEnvironmentTypes is the canonical, exhaustive list of environment
// types. IsValid and the completeness test in config_test.go both walk this
// slice, so a fifth type added here without an explicit case in every
// exclusion-shaped predicate fails that test rather than silently falling
// into whichever branch nobody updated.
var validEnvironmentTypes = []EnvironmentType{
	EnvironmentTypeLocalAgent,
	EnvironmentTypeRemoteAgent,
	EnvironmentTypeRuntime,
	EnvironmentTypeHost,
}

// IsValid reports whether the value is one of the canonical types. Empty is
// not valid; callers wanting "unset" should test against the zero value
// separately and then resolve via EnvConfig.ResolvedType.
func (t EnvironmentType) IsValid() bool {
	for _, valid := range validEnvironmentTypes {
		if t == valid {
			return true
		}
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
	RuntimeImage string `yaml:"runtimeimage,omitempty" json:"runtimeImage,omitempty"`
	// RuntimeRunningImage is a display-only memo of the runtime image a deploy
	// last actually resolved for this env's pod (imageOverrides.erun-devops, or
	// the chart's own stock default when no override applied) -- healed
	// alongside RuntimeVersion by PersistRuntimeVersionFromDeploySpecs. `erun
	// list` reads it to name which release line RuntimeVersion's number
	// belongs to (erun's own vs. a tenant's own <tenant>-devops line; see
	// erun#1746). Unlike RuntimeImage above, it is never read back to
	// influence a deploy's own image choice, and stays empty whenever the
	// resolution path that produced the deploy could not know the image (a
	// repo-local runtime chart's own values decide it) -- callers must render
	// that as undetermined, never guess a line from the tenant name alone.
	RuntimeRunningImage string `yaml:"runtimerunningimage,omitempty" json:"runtimeRunningImage,omitempty"`
	// RuntimeChart names the runtime chart this env rides, as an OCI reference
	// that may carry its own version. The chart and the runtime image are separate
	// artifacts on separate lines: the chart is erun's, published at erun's
	// versions, while RuntimeImage may be a project's own, versioned on the
	// project's line. Deriving both from RuntimeVersion can only name one line, so
	// an env whose image rides its own states the chart here. Empty keeps the
	// published lookup at the runtime version, which is right whenever push
	// published the pair together.
	RuntimeChart string `yaml:"runtimechart,omitempty" json:"runtimeChart,omitempty"`
	// MCPAuthPublicKeyPath records the desktop public key a deploy last enabled
	// MCP auth with, so a later redeploy that does not re-supply the key rethreads
	// it instead of falling back to the chart default and silently turning the
	// env's authenticated MCP edge into an unauthenticated one. Cleared only by an
	// explicit opt-out; empty means the env never had desktop MCP auth.
	MCPAuthPublicKeyPath string `yaml:"mcpauthpublickeypath,omitempty" json:"mcpAuthPublicKeyPath,omitempty"`
	// ImagePullSecrets names Kubernetes dockerconfigjson secrets the runtime pod
	// uses to pull private images (e.g. a private <tenant>-devops umbrella image).
	// Empty leaves the pod pulling anonymously, so envs on public images (the erun
	// product tenant's own) are unaffected. erun references an operator-provisioned
	// secret by name; it does not create the credential.
	ImagePullSecrets []string `yaml:"imagepullsecrets,omitempty" json:"imagePullSecrets,omitempty"`
	// RegistryCredentialSecretName names a Kubernetes dockerconfigjson Secret that
	// `erun init` minted from the host's own resolved ghcr.io credential, so the
	// pod it deploys can read from and push to a registry it has never
	// authenticated to on its own. Empty means init found no host
	// credential to provision; an existing in-pod credential, if any, is
	// unaffected. Only init mints or rotates this value -- a plain `erun deploy`
	// only carries the persisted name forward.
	RegistryCredentialSecretName string              `yaml:"registrycredentialsecretname,omitempty" json:"registryCredentialSecretName,omitempty"`
	RuntimePod                   RuntimePodResources `yaml:"runtimepod,omitempty"`
	// NamespaceQuota is a hard per-namespace ceiling (ResourceQuota + LimitRange)
	// distinct from RuntimePod's own-container sizing; see NamespaceResourceQuota.
	NamespaceQuota      NamespaceResourceQuota  `yaml:"namespacequota,omitempty" json:"namespaceQuota,omitempty"`
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
	// PlatformAccount designates this env's runtime ServiceAccount as the
	// cluster's erun platform admin: deploy binds it to the built-in
	// cluster-admin ClusterRole so in-pod `erun terraform apply` (the cluster
	// edge) and platform component installs (cert-manager, Traefik, PowerDNS)
	// can create the cluster-scoped resources they require. Default false leaves
	// the SA with namespaced admin only.
	PlatformAccount bool `yaml:"platformaccount,omitempty" json:"platformAccount,omitempty"`
	// Stopped records that the operator stopped this environment, so its runtime
	// Deployment stays scaled to zero and its node capacity stays with the
	// environments actually in use. It is the durable half of the stop: a bare
	// scale patch is drift a helm upgrade silently reverts, so `deploy` renders
	// the chart's `stopped` value from this flag and reconciles replicas
	// declaratively. `erun open` clears it — waking is what opening means.
	Stopped bool `yaml:"stopped,omitempty" json:"stopped,omitempty"`
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

// BuildsHere reports whether builds happen inside this env (local-agent,
// remote-agent, and host build here; runtime envs only receive deploys). An
// env whose type is unresolved is treated as not building here. Every valid
// type has an explicit case — see validEnvironmentTypes — so a type added
// without updating this switch panics instead of silently taking a default.
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

// RemoteWorktree reports whether the worktree lives outside the local
// machine (PVC for remote-agent, none for runtime). Local-agent and host both
// mount/use the local filesystem directly — the only difference between them
// is whether a pod exists at all. An env whose type is unresolved is treated
// as not having a remote worktree. Every valid type has an explicit case —
// see validEnvironmentTypes — so a type added without updating this switch
// panics instead of silently taking a default.
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

// HasPod reports whether this env has a runtime pod at all. Every type but
// host does; host is the one type with no pod and no cluster contact of any
// kind. Operations that are meaningless without a pod (deploy, push, pin,
// terraform, port-forwards, runtime-pod diagnostics) must check this — or the
// narrower predicate that already applies to their own decision — and refuse
// explicitly rather than resolving a plan that cannot run.
func (c EnvConfig) HasPod() bool {
	return c.Type.IsValid() && c.Type != EnvironmentTypeHost
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

// RuntimeImageRegistryMismatch reports the registries `erun doctor` compares
// to catch a pod that can never pull: a persisted RuntimeImage naming a
// registry other than the env's own RuntimeRegistry. The pull secret erun
// refreshes on every deploy is keyed off exactly the registries in play
// (containerRegistry, which follows RuntimeRegistry when the env records one,
// plus each imageOverrides registry) — but a credential still has to resolve
// for both at deploy time, so a RuntimeImage on a different registry than
// RuntimeRegistry is worth flagging even when the refresh above already
// tries to cover it. mismatched is false when the image carries no registry
// of its own (a bare name follows RuntimeRegistry, nothing to compare) or the
// env records no RuntimeRegistry to compare against.
func (c EnvConfig) RuntimeImageRegistryMismatch() (imageRegistry, runtimeRegistry string, mismatched bool) {
	imageRegistry = runtimeImageRegistry(c.RuntimeImage)
	runtimeRegistry = strings.TrimSpace(c.RuntimeRegistry)
	if imageRegistry == "" || runtimeRegistry == "" {
		return imageRegistry, runtimeRegistry, false
	}
	return imageRegistry, runtimeRegistry, !strings.EqualFold(imageRegistry, runtimeRegistry)
}

// RuntimeImageLineMismatch reports whether the operative RuntimeImage (the
// value a future deploy reads back to pick the runtime pod's image) and the
// observed RuntimeRunningImage (the last image a deploy actually confirmed
// running, healed alongside RuntimeVersion by
// PersistRuntimeVersionFromDeploySpecs) name different release lines --
// stock erun-devops vs a tenant's own <tenant>-devops. A version number alone
// cannot name a line (the same number is valid on both), but an image name
// always can, so this needs no live cluster read: it is the persisted half
// of the pairing erun#1754 was filed over, readable from config alone.
// mismatched is false whenever either field is empty or fails to parse as a
// runtime image reference -- an environment with no recorded history yet has
// nothing to disagree with, and this must never guess.
func (c EnvConfig) RuntimeImageLineMismatch() (recordedLine, observedLine string, mismatched bool) {
	recordedLine, recordedOK := runtimeImageReleaseLine(c.RuntimeImage)
	observedLine, observedOK := runtimeImageReleaseLine(c.RuntimeRunningImage)
	if !recordedOK || !observedOK {
		return recordedLine, observedLine, false
	}
	return recordedLine, observedLine, recordedLine != observedLine
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
// "stable", agent envs and host envs track "snapshot" (they iterate on
// snapshot builds) — and anything unresolved falls back to "stable". A host
// env never actually upgrades (it has no runtime to redeploy — see
// EnvConfig.HasPod), so this only matters if AutoUpgrade is set on one, which
// the upgrade planner skips outright.
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

// EffectiveLocalRepoPath returns the env's host-machine repo path. The legacy
// `repopath` key folds into LocalRepoPath on read, so callers wanting the host
// path should use this helper rather than reading the field directly.
func (c EnvConfig) EffectiveLocalRepoPath() string {
	return strings.TrimSpace(c.LocalRepoPath)
}

// TenantLocalCheckoutRoot finds a tenant's repo on this machine through one of
// its other environments, for a caller whose own target environment has no
// worktree here — a sourceless runtime env (no MountSource) chief among them.
// Every environment of a tenant shares the same repo, so a sibling env whose
// worktree lives on this host (RemoteWorktree false) and whose recorded path
// still exists on disk is the tenant's own checkout, not a guess from the
// caller's unrelated working directory.
func TenantLocalCheckoutRoot(envs []EnvConfig) (string, bool) {
	for _, candidate := range envs {
		if candidate.RemoteWorktree() {
			continue
		}
		path := candidate.EffectiveLocalRepoPath()
		if path == "" {
			continue
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			continue
		}
		return path, true
	}
	return "", false
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
	// Platforms pins the docker --platform targets a non-release build/push mints
	// for this environment (e.g. ["linux/amd64"]), for an environment whose
	// cluster can only ever run one architecture. It never applies to a release
	// build (`erun build --release`, `erun release`): those always publish every
	// platform erun supports, since a release artifact must be deployable
	// anywhere. Empty keeps the default multi-arch build.
	Platforms []string `yaml:"platforms,omitempty"`
}

func (c ProjectDockerConfig) IsZero() bool {
	return len(c.Fingerprints) == 0 && len(c.Platforms) == 0
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

// DockerPlatformsForEnvironment returns the configured docker --platform targets
// for the given environment, or nil when none is set (keeping the default
// multi-arch build).
func (c ProjectConfig) DockerPlatformsForEnvironment(environment string) []string {
	environment = strings.TrimSpace(environment)
	if environment == "" || c.Environments == nil {
		return nil
	}
	envConfig, ok := c.Environments[environment]
	if !ok || len(envConfig.Docker.Platforms) == 0 {
		return nil
	}
	out := make([]string, 0, len(envConfig.Docker.Platforms))
	for _, platform := range envConfig.Docker.Platforms {
		if platform = strings.TrimSpace(platform); platform != "" {
			out = append(out, platform)
		}
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

	existing, _ := os.ReadFile(configFilePath)
	data, err := marshalConfigPreservingUnknownFields(existing, config)
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

	// A zero-length or unparseable file is the residue of an interrupted
	// write, possibly one not even erun's own -- loadConfigFile retries it a
	// few times before giving up. Treating it as "successfully loaded empty
	// config" lets the next writer rebuild a fresh ERunConfig{} with only the
	// field it cares about, silently dropping every other section, so a read
	// that never recovers is surfaced as corruption instead, routing callers
	// into the doctor recovery path.
	if err := loadConfigFile(configFilePath, &config); err != nil {
		return config, configFilePath, err
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

	existing, _ := os.ReadFile(configFilePath)
	data, err := marshalConfigPreservingUnknownFields(existing, config)
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

	if err := loadConfigFile(configFilePath, &config); err != nil {
		return config, configFilePath, err
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

	existing, _ := os.ReadFile(configFilePath)
	data, err := marshalConfigPreservingUnknownFields(existing, config)
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

	if err := loadConfigFile(configFilePath, &config); err != nil {
		return config, configFilePath, err
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

	existing, _ := os.ReadFile(configFilePath)
	data, err := marshalConfigPreservingUnknownFields(existing, config)
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

	if err := loadConfigFile(configFilePath, &config); err != nil {
		return config, configFilePath, err
	}

	return config, configFilePath, nil
}

// FindProjectRoot resolves the project root, preferring ERUN_REPO_PATH when it
// is set and names an existing directory. A sourceless runtime pod surfaces the
// image-baked release tree at that path (a symlink into /opt/erun/release, with
// no .git), so the cwd .git walk would fail there; honoring the env var lets
// in-pod commands like `erun terraform` resolve the tree the same way the MCP
// server does. The var is set only inside the pod, so laptop behavior (walk up
// from cwd to the nearest .git) is unchanged.
func FindProjectRoot() (string, string, error) {
	if root := strings.TrimSpace(os.Getenv("ERUN_REPO_PATH")); root != "" {
		root = filepath.Clean(root)
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return filepath.Base(root), root, nil
		}
	}
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

// configReadRetryAttempts/configReadRetrySleep bound how long a read waits out
// a torn write before surfacing ErrConfigCorrupted. writeFileAtomic makes erun's
// own writers atomic, but a reader still has to tolerate a write it does not
// control -- an external editor saving in place, another process's own
// non-atomic writer, a crash mid-write -- and a torn read from any of those is
// transient by construction: it resolves itself within microseconds, well
// under this budget. A file that still fails to parse after every retry is
// genuinely corrupt, not merely caught mid-write.
const (
	configReadRetryAttempts = 5
	configReadRetrySleep    = 20 * time.Millisecond
)

// loadConfigFile reads path and unmarshals it into out, retrying through
// configReadRetryAttempts when the file is momentarily empty or fails to
// parse. Every Load*Config function in this file routes its read through
// here so the retry policy lives in one place. A missing file is a real
// absence (ErrNotInitialized) and is never retried; an empty or unparseable
// file is retried and, if it never recovers, reported as ErrConfigCorrupted.
func loadConfigFile(path string, out any) error {
	var lastErr error
	for attempt := 0; attempt < configReadRetryAttempts; attempt++ {
		data, err := os.ReadFile(path)
		if err != nil {
			return ErrNotInitialized
		}
		if len(bytes.TrimSpace(data)) == 0 {
			lastErr = ErrConfigCorrupted
		} else if err := yaml.Unmarshal(data, out); err != nil {
			lastErr = fmt.Errorf("%w: %v", ErrConfigCorrupted, err)
		} else {
			return nil
		}
		if attempt < configReadRetryAttempts-1 {
			time.Sleep(configReadRetrySleep)
		}
	}
	return lastErr
}
