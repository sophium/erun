package eruncommon

import (
	"fmt"
	"os"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

type ListStore interface {
	configpkg.OpenStore
	ListTenantConfigs() ([]configpkg.TenantConfig, error)
	ListEnvConfigs(string) ([]configpkg.EnvConfig, error)
}

type ListEnvironmentResult struct {
	Name                string                        `json:"name"`
	Type                configpkg.EnvironmentType     `json:"type,omitempty"`
	APIURL              string                        `json:"apiUrl,omitempty"`
	KubernetesContext   string                        `json:"kubernetesContext,omitempty"`
	CloudProviderAlias  string                        `json:"cloudProviderAlias,omitempty"`
	RepoPath            string                        `json:"repoPath,omitempty"`
	LocalRepoPath       string                        `json:"localRepoPath,omitempty"`
	ContainerRegistries configpkg.ContainerRegistries `json:"containerRegistries,omitempty"`
	RuntimeVersion      string                        `json:"runtimeVersion,omitempty"`
	// RuntimeVersionLine names which release line RuntimeVersion's number
	// belongs to. Nil whenever RuntimeVersion itself is empty -- there is
	// nothing to annotate for an environment that has never deployed.
	RuntimeVersionLine *RuntimeVersionLine `json:"runtimeVersionLine,omitempty"`
	// ErunVersion is the erun version this environment's runtime chart carries
	// -- see ResolveErunVersion. Nil whenever it cannot be read from config
	// alone, including whenever RuntimeVersion itself is empty.
	ErunVersion *ErunVersion `json:"erunVersion,omitempty"`
	// RuntimeImageLineMismatch is set only when the environment's recorded and
	// last-observed runtime images name different release lines -- see
	// EnvConfig.RuntimeImageLineMismatch.
	RuntimeImageLineMismatch *RuntimeImageLineMismatchResult `json:"runtimeImageLineMismatch,omitempty"`
	RuntimePod               configpkg.RuntimePodResources   `json:"runtimePod,omitempty"`
	// Sizing is the environment's standing recommendation, derived from the usage
	// history the in-pod monitor retains. Nil where erun has never observed this
	// environment — which is every environment seen from a host other than its
	// own runtime container, since the history is written by the container that
	// produced it.
	Sizing       *RuntimeSizingRecommendation `json:"sizing,omitempty"`
	ManagedCloud bool                         `json:"managedCloud,omitempty"`
	// Hosted is the platform row this environment corresponds to, when it has
	// one. Nil means the environment is not marked as hosted, which is the
	// normal state: most environments never reach the platform.
	Hosted             *configpkg.HostedEnvironment      `json:"hosted,omitempty"`
	DisableBuildScript bool                              `json:"disableBuildScript,omitempty"`
	PlatformAccount    bool                              `json:"platformAccount,omitempty"`
	AITool             string                            `json:"aiTool,omitempty"`
	Claude             configpkg.EnvironmentClaudeConfig `json:"claude,omitempty"`
	Idle               configpkg.EnvironmentIdleConfig   `json:"idle,omitempty"`
	Deploy             configpkg.EnvironmentDeployConfig `json:"deploy,omitempty"`
	IsActive           bool                              `json:"isActive,omitempty"`
	LocalPorts         configpkg.EnvironmentLocalPorts   `json:"localPorts,omitempty"`
	IsDefault          bool                              `json:"isDefault,omitempty"`
	IsEffective        bool                              `json:"isEffective,omitempty"`
	SSH                ListSSHResult                     `json:"ssh,omitempty"`
	AutoStart          *bool                             `json:"autoStart,omitempty"`
}

// RuntimeImageLineMismatchResult is the list read-model view of
// EnvConfig.RuntimeImageLineMismatch, present only when it reports a real
// disagreement -- an environment with no recorded history, or one whose
// recorded and observed images agree, has nothing to surface.
type RuntimeImageLineMismatchResult struct {
	RecordedLine string `json:"recordedLine"`
	ObservedLine string `json:"observedLine"`
}

type ListSSHResult struct {
	Enabled                bool   `json:"enabled,omitempty"`
	HostAlias              string `json:"hostAlias,omitempty"`
	User                   string `json:"user,omitempty"`
	LocalPort              int    `json:"localPort,omitempty"`
	WorkspacePath          string `json:"workspacePath,omitempty"`
	PublicKeyPath          string `json:"publicKeyPath,omitempty"`
	WorkspaceSyncEnabled   bool   `json:"workspaceSyncEnabled,omitempty"`
	WorkspaceSyncLocalPath string `json:"workspaceSyncLocalPath,omitempty"`
}

func NewListEnvironmentResult(store ListStore, tenant configpkg.TenantConfig, env configpkg.EnvConfig, effective configpkg.OpenResult, effectiveErr error, portAllocations map[string]configpkg.EnvironmentLocalPorts) ListEnvironmentResult {
	localPorts := listEnvironmentLocalPorts(tenant.Name, env, portAllocations)
	runtimeVersionLine := listRuntimeVersionLine(tenant.Name, env)
	return ListEnvironmentResult{
		Name:                     env.Name,
		Type:                     env.ResolvedType(),
		APIURL:                   APIURLForListEnvironment(tenant, localPorts),
		KubernetesContext:        strings.TrimSpace(env.KubernetesContext),
		CloudProviderAlias:       strings.TrimSpace(env.CloudProviderAlias),
		RepoPath:                 env.EffectiveLocalRepoPath(),
		LocalRepoPath:            strings.TrimSpace(env.LocalRepoPath),
		ContainerRegistries:      configpkg.EffectiveEnvironmentContainerRegistries(env),
		RuntimeVersion:           strings.TrimSpace(env.RuntimeVersion),
		RuntimeVersionLine:       runtimeVersionLine,
		ErunVersion:              ResolveErunVersion(env, runtimeVersionLine),
		RuntimeImageLineMismatch: listRuntimeImageLineMismatch(env),
		RuntimePod:               env.RuntimePod,
		Sizing:                   EnvironmentRuntimeSizing(tenant.Name, env),
		ManagedCloud:             env.ManagedCloud,
		Hosted:                   listHostedEnvironment(env),
		DisableBuildScript:       env.DisableBuildScript,
		PlatformAccount:          env.PlatformAccount,
		AITool:                   strings.TrimSpace(env.AITool),
		Claude:                   env.Claude,
		Idle:                     env.Idle,
		Deploy:                   env.Deploy,
		IsActive:                 listEnvironmentIsActive(store, env),
		LocalPorts:               localPorts,
		IsDefault:                env.Name == tenant.DefaultEnvironment,
		IsEffective:              effectiveErr == nil && tenant.Name == effective.Tenant && env.Name == effective.Environment,
		SSH:                      NewListSSHResult(listEnvironmentOpenResult(tenant, env, localPorts)),
		AutoStart:                copyAutoStartPtr(env.AutoStart),
	}
}

// listHostedEnvironment returns the env's hosted marker as a pointer, so an
// environment that carries none reports absent rather than as an all-zero
// marker a reader would have to know to ignore.
func listHostedEnvironment(env configpkg.EnvConfig) *configpkg.HostedEnvironment {
	marker, ok := configpkg.HostedEnvironmentFromConfig(env)
	if !ok {
		return nil
	}
	return &marker
}

// listRuntimeVersionLine wraps ResolveRuntimeVersionLine, but only when there
// is a RuntimeVersion to annotate at all -- an environment that has never
// deployed has no version, and "undetermined" would misread as "deployed,
// but the line is unknown" rather than "never deployed".
func listRuntimeVersionLine(tenant string, env configpkg.EnvConfig) *RuntimeVersionLine {
	if strings.TrimSpace(env.RuntimeVersion) == "" {
		return nil
	}
	line := ResolveRuntimeVersionLine(tenant, env)
	return &line
}

// listRuntimeImageLineMismatch wraps EnvConfig.RuntimeImageLineMismatch, only
// surfacing a result when it reports a real disagreement.
func listRuntimeImageLineMismatch(env configpkg.EnvConfig) *RuntimeImageLineMismatchResult {
	recordedLine, observedLine, mismatched := env.RuntimeImageLineMismatch()
	if !mismatched {
		return nil
	}
	return &RuntimeImageLineMismatchResult{RecordedLine: recordedLine, ObservedLine: observedLine}
}

// EnvironmentRuntimeSizing attaches the standing recommendation when there is
// one. A read failure is silence rather than an error: every caller of this
// (`erun list`, `resize`, the `usage` MCP tool) treats sizing as advisory and
// must not fail over it.
func EnvironmentRuntimeSizing(tenant string, env configpkg.EnvConfig) *RuntimeSizingRecommendation {
	history, err := LoadRuntimeUsageHistory(tenant, env.Name)
	if err != nil {
		return nil
	}
	recommendation, ok := RecommendRuntimeSizing(RuntimeSizingParams{History: history, Ceiling: env.NamespaceQuota})
	if !ok {
		return nil
	}
	return &recommendation
}

func copyAutoStartPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	v := *value
	return &v
}

func APIURLForListEnvironment(tenant configpkg.TenantConfig, localPorts configpkg.EnvironmentLocalPorts) string {
	if apiURL := strings.TrimSpace(tenant.APIURL); apiURL != "" {
		return apiURL
	}
	port := localPorts.API
	if port <= 0 {
		port = configpkg.APIServicePort
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// listEnvironmentLocalPorts resolves the ports this process should report for
// one environment: the config-derived allocation, overridden by the chart's
// injected ERUN_*_PORT values when this process is that environment's own
// runtime pod -- see overlayInjectedRuntimeLocalPorts.
func listEnvironmentLocalPorts(tenant string, env configpkg.EnvConfig, portAllocations map[string]configpkg.EnvironmentLocalPorts) configpkg.EnvironmentLocalPorts {
	localPorts := portAllocations[configpkg.EnvironmentPortKey(tenant, env.Name)]
	if env.SSHD.LocalPort > 0 {
		localPorts.SSH = env.SSHD.LocalPort
	}
	return configpkg.OverlayInjectedRuntimeLocalPorts(localPorts, os.Getenv, tenant, env.Name)
}

func listEnvironmentOpenResult(tenant configpkg.TenantConfig, env configpkg.EnvConfig, localPorts configpkg.EnvironmentLocalPorts) configpkg.OpenResult {
	return configpkg.OpenResult{
		Tenant:      tenant.Name,
		Environment: env.Name,
		TenantConfig: configpkg.TenantConfig{
			Name: tenant.Name,
		},
		EnvConfig:  env,
		LocalPorts: localPorts,
		RepoPath:   env.EffectiveLocalRepoPath(),
	}
}

func listEnvironmentIsActive(store configpkg.CloudReadStore, env configpkg.EnvConfig) bool {
	if !env.RemoteWorktree() {
		return false
	}
	status, ok, err := configpkg.FindCloudContextForKubernetesContext(store, env.KubernetesContext)
	if err != nil || !ok {
		return false
	}
	return strings.TrimSpace(status.Status) == cloud.CloudContextStatusRunning
}

func NewListSSHResult(result configpkg.OpenResult) ListSSHResult {
	if !result.EnvConfig.SSHD.Enabled {
		return ListSSHResult{}
	}

	info := SSHConnectionInfoForResult(result)
	sync := result.EnvConfig.SSHD.WorkspaceSync
	return ListSSHResult{
		Enabled:                true,
		HostAlias:              info.HostAlias,
		User:                   info.User,
		LocalPort:              info.Port,
		WorkspacePath:          info.WorkspacePath,
		PublicKeyPath:          strings.TrimSpace(result.EnvConfig.SSHD.PublicKeyPath),
		WorkspaceSyncEnabled:   sync.Enabled,
		WorkspaceSyncLocalPath: strings.TrimSpace(sync.LocalPath),
	}
}
