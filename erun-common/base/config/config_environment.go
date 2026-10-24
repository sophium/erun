package config

import (
	"fmt"
	"os"
	"strings"
)

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

// ValidEnvironmentTypes is the canonical, exhaustive list of environment
// types. IsValid and the completeness test in config_test.go both walk this
// slice, so a fifth type added here without an explicit case in every
// exclusion-shaped predicate fails that test rather than silently falling
// into whichever branch nobody updated.
var ValidEnvironmentTypes = []EnvironmentType{
	EnvironmentTypeLocalAgent,
	EnvironmentTypeRemoteAgent,
	EnvironmentTypeRuntime,
	EnvironmentTypeHost,
}

// IsValid reports whether the value is one of the canonical types. Empty is
// not valid; callers wanting "unset" should test against the zero value
// separately and then resolve via EnvConfig.ResolvedType.
func (t EnvironmentType) IsValid() bool {
	for _, valid := range ValidEnvironmentTypes {
		if t == valid {
			return true
		}
	}
	return false
}

// UsesDindSidecar reports whether an environment of this type carries the
// erun-dind sidecar every image build actually runs in -- mirrors
// $dindEnabled in erun-devops/k8s/erun-devops/templates/service.yaml (`ne
// $envType "runtime"`), narrowed by the one case that chart condition doesn't
// cover: a host env renders no pod at all. This is the one signal that tells
// a runtime-container-scoped reading (RunRuntimeUsage) whether it is leaving
// out a real, separate cgroup where builds spend CPU/memory -- see
// environmentUsesDindSidecar in
// erun-ui/frontend/src/components/app/Sidebar.helpers.ts, which this mirrors;
// keep both in sync.
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
	// Hosted records which platform row this environment corresponds to, so a
	// definition can be uploaded to and pulled from the right one. It is a
	// local assertion, not server state, and it is deliberately separate from
	// ManagedCloud above — see HostedEnvironment's own comment.
	Hosted          HostedEnvironment `yaml:"hosted,omitempty" json:"hosted,omitempty"`
	RuntimeVersion  string            `yaml:"runtimeversion,omitempty"`
	RuntimeRegistry string            `yaml:"runtimeregistry,omitempty" json:"runtimeRegistry,omitempty"`
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
	RegistryCredentialSecretName string `yaml:"registrycredentialsecretname,omitempty" json:"registryCredentialSecretName,omitempty"`
	// PlatformAliasSecretName names a Kubernetes Secret that `erun init` minted
	// from the host's own signed-in erun platform cloud provider alias, so the
	// pod it deploys can call the platform API (the merge queue's read and
	// self-report routes among them) without a human completing an interactive
	// OIDC login inside it. Empty means init found no signed-in host alias to
	// provision. Only init mints or rotates this value -- a plain `erun deploy`
	// only carries the persisted name forward.
	PlatformAliasSecretName string              `yaml:"platformaliassecretname,omitempty" json:"platformAliasSecretName,omitempty"`
	RuntimePod              RuntimePodResources `yaml:"runtimepod,omitempty"`
	// RuntimeDindPod sizes the erun-dind sidecar's own CPU/memory limits,
	// independent of RuntimePod which only sizes the runtime container itself.
	// Zero falls back to DefaultRuntimeDindCPU/Memory (NormalizeRuntimeDindPodResources).
	RuntimeDindPod RuntimePodResources `yaml:"runtimedindpod,omitempty"`
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
	imageRegistry = RuntimeImageRegistry(c.RuntimeImage)
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
	recordedLine, recordedOK := RuntimeImageReleaseLine(c.RuntimeImage)
	observedLine, observedOK := RuntimeImageReleaseLine(c.RuntimeRunningImage)
	if !recordedOK || !observedOK {
		return recordedLine, observedLine, false
	}
	return recordedLine, observedLine, recordedLine != observedLine
}

// LegacyEnvTypeFromRemoteSnapshot migrates configs written before the `type`
// field existed, mapping the old remote+snapshot pair to a concrete type. It must
// reproduce the old deciders exactly: a missing snapshot key meant "does not build
// here", and the one non-remote/non-building combo has no concrete type so its
// deciders stay false on both axes as before.
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
