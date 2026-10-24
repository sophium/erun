package open

import (
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

type BootstrapInitParams struct {
	Tenant                   string
	SelectedTenant           string
	InitializeCurrentProject bool
	ProjectRoot              string
	Environment              string
	RuntimeVersion           string
	// RuntimeVersionDefault is the version an env gets when it has none of its
	// own — the transport's own build version. It is kept apart from
	// RuntimeVersion because a transport-supplied fallback is not an operator
	// asking for a version: folding the two together would repin an existing env
	// to whatever erun the operator happened to run, on an invocation about
	// something else entirely.
	RuntimeVersionDefault string
	RuntimeImage          string
	// RuntimeRegistry redirects the environment's runtime chart and its in-pod
	// platform image resolution to the registry erun publishes them in, for an
	// env whose deploy registry holds only its own app images. It is the
	// supported way in: resolution honours the field first, but every other
	// writer of it is a side effect of a deploy that already succeeded, which an
	// env in that state cannot get.
	RuntimeRegistry string
	// ImagePullSecrets names the dockerconfigjson secrets the runtime pod pulls
	// its image with, which a private runtime image cannot start without.
	ImagePullSecrets []string
	RuntimePod       configpkg.RuntimePodResources
	// RuntimeDindPod sizes the erun-dind sidecar's own CPU/memory limits,
	// independent of RuntimePod which only sizes the runtime container itself.
	RuntimeDindPod    configpkg.RuntimePodResources
	NoGit             bool
	KubernetesContext string
	ContainerRegistry string
	// ClusterRegistry, when set, seeds the new env with a resolvable cluster:
	// registry entry (addresses resolved from the env's kube-context) instead of
	// the static ContainerRegistry string. Set by `erun init --cluster-registry`.
	ClusterRegistry *configpkg.ClusterRegistry
	// ErunRegistry, when true, seeds the new env with erun's hosted registry
	// (HostedRegistryReference(tenant)) instead of the static ContainerRegistry
	// string. Set by `erun init --erun-registry`; mutually exclusive with
	// ContainerRegistry and ClusterRegistry.
	ErunRegistry bool
	// MCPAuthPublicKeyPath, when set, points at a PEM public key the init-time
	// runtime deploy trusts so the env's erun-mcp edge requires a bearer signed by
	// it — mirrors `erun deploy --mcp-auth-public-key`. The desktop sets it so
	// init's single deploy already carries MCP auth and no post-init redeploy
	// (which would roll the just-created pod) is needed.
	MCPAuthPublicKeyPath string
	// Type is the canonical environment type and takes precedence over the
	// legacy Remote bool; when unset the type is derived from Remote for
	// backward compatibility with --remote flag callers.
	Type                    configpkg.EnvironmentType
	Remote                  bool
	RemoteRepositoryURL     string
	CodeCommitSSHKeyID      string
	Bootstrap               bool
	ConfirmTenant           *bool
	ConfirmEnvironment      *bool
	ConfirmRemoteHostConfig *bool
	ConfirmRemoteKeyImport  *bool
	AutoApprove             bool
	ResolveTenant           bool
	DisableBuildScript      bool
	PlatformAccount         bool
	// Components sets the env's saved deploy.components default selection —
	// what `erun deploy` rolls out when no --components flag is passed. nil
	// means the invocation named nothing and the saved selection (if any) is
	// left untouched; a non-nil, empty slice is the explicit way to clear a
	// saved selection and return the env to its repo k8s.deployments plan —
	// the ambiguity plain-string flags have between "omitted" and
	// "explicitly emptied" does not apply here because the pointer itself
	// carries that distinction.
	Components *[]string
}

// ResolvedType returns the type a *new* env is created with: an explicit Type
// wins, otherwise it is derived from Remote to preserve the pre-type behavior of
// `erun init [--remote]`, and an invocation that names neither falls back to
// local-agent. It answers "what kind of env is this run creating", never "what
// kind is the env on disk" — for an existing env see RequestedType.
func (p BootstrapInitParams) ResolvedType() configpkg.EnvironmentType {
	if p.Type.IsValid() {
		return p.Type
	}
	if p.Remote {
		return configpkg.EnvironmentTypeRemoteAgent
	}
	return configpkg.EnvironmentTypeLocalAgent
}

// RequestedType reports the type the operator actually named, and whether they
// named one at all. The fallback ResolvedType applies is a default, not a
// request: retyping an existing env because a flag was omitted would be a worse
// silent write than the dropped settings it would be repairing.
func (p BootstrapInitParams) RequestedType() (configpkg.EnvironmentType, bool) {
	if p.Type.IsValid() {
		return p.Type, true
	}
	if p.Remote {
		return configpkg.EnvironmentTypeRemoteAgent, true
	}
	return "", false
}

// ResolvedRuntimeVersion is the version a new env is pinned to: the operator's
// --version, or the transport's own build version when they named none.
func (p BootstrapInitParams) ResolvedRuntimeVersion() string {
	if version := strings.TrimSpace(p.RuntimeVersion); version != "" {
		return version
	}
	return strings.TrimSpace(p.RuntimeVersionDefault)
}

// RemoteWorktree reports whether the new env's worktree will live outside the
// local machine. Mirrors EnvConfig.RemoteWorktree's explicit-by-type
// enumeration: local-agent and host both use the local machine's own
// filesystem, so both answer false.
func (p BootstrapInitParams) RemoteWorktree() bool {
	switch p.ResolvedType() {
	case configpkg.EnvironmentTypeRemoteAgent, configpkg.EnvironmentTypeRuntime:
		return true
	case configpkg.EnvironmentTypeLocalAgent, configpkg.EnvironmentTypeHost:
		return false
	default:
		panic(fmt.Sprintf("RemoteWorktree: unhandled EnvironmentType %q", p.ResolvedType()))
	}
}
