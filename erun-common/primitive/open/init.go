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
	// RuntimeVersionDefault is the transport's build version; kept apart from
	// RuntimeVersion so a fallback never repins an existing env.
	RuntimeVersionDefault string
	RuntimeImage          string
	// RuntimeRegistry is where erun's runtime chart and platform images resolve
	// from when the deploy registry holds only app images.
	RuntimeRegistry  string
	ImagePullSecrets []string
	RuntimePod       configpkg.RuntimePodResources
	// RuntimeDindPod sizes the erun-dind sidecar, independently of RuntimePod.
	RuntimeDindPod    configpkg.RuntimePodResources
	NoGit             bool
	KubernetesContext string
	ContainerRegistry string
	ClusterRegistry   *configpkg.ClusterRegistry
	// ErunRegistry is mutually exclusive with ContainerRegistry and ClusterRegistry.
	ErunRegistry bool
	// MCPAuthPublicKeyPath lets init's single deploy carry MCP auth, avoiding a
	// redeploy that would roll the new pod.
	MCPAuthPublicKeyPath string
	// Type takes precedence over the legacy Remote bool.
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
	// Components is the saved deploy.components selection: nil leaves it
	// untouched, an empty slice clears it.
	Components *[]string
}

// ResolvedType is the type a new env is created with; for an existing env see
// RequestedType.
func (p BootstrapInitParams) ResolvedType() configpkg.EnvironmentType {
	if p.Type.IsValid() {
		return p.Type
	}
	if p.Remote {
		return configpkg.EnvironmentTypeRemoteAgent
	}
	return configpkg.EnvironmentTypeLocalAgent
}

// RequestedType reports only an explicitly named type, so an omitted flag never
// retypes an existing env.
func (p BootstrapInitParams) RequestedType() (configpkg.EnvironmentType, bool) {
	if p.Type.IsValid() {
		return p.Type, true
	}
	if p.Remote {
		return configpkg.EnvironmentTypeRemoteAgent, true
	}
	return "", false
}

// ResolvedRuntimeVersion is the version a new env is pinned to.
func (p BootstrapInitParams) ResolvedRuntimeVersion() string {
	if version := strings.TrimSpace(p.RuntimeVersion); version != "" {
		return version
	}
	return strings.TrimSpace(p.RuntimeVersionDefault)
}

// RemoteWorktree reports whether the new env's worktree lives off this machine.
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
