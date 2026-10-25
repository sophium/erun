package helmdeploy

import (
	"io"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// HelmReleaseVersionResolverFunc reads a deployed release's appVersion.
type HelmReleaseVersionResolverFunc func(ctx core.Context, releaseName, namespace, kubernetesContext string) (string, error)

type (
	HelmChartDeployerFunc    func(HelmDeployParams) error
	HelmReleaseRecovererFunc func(HelmReleaseRecoveryParams) error
)

// HelmDeployParams is a resolved deploy's execution view: the full HelmDeploySpec plus output
// writers. It embeds the spec so no field can be dropped between trace and execution.
type HelmDeployParams struct {
	HelmDeploySpec
	Stdout io.Writer
	Stderr io.Writer
}

type HelmDeploySpec struct {
	ReleaseName    string
	ChartPath      string
	ValuesFilePath string
	// PulledValuesFilePath is a by-reference umbrella's bundled values.<env>.yaml, -f'd before
	// ValuesFilePath; helm applies only a chart's default values.yaml. Set by RunHelmDeploy.
	PulledValuesFilePath string
	// SubchartKey prefixes every --set key for an umbrella wrapping a canonical erun-<base> chart,
	// since helm does not pass top-level values into subchart scope. Empty for a top-level chart.
	SubchartKey       string
	Tenant            string
	Environment       string
	Namespace         string
	KubernetesContext string
	WorktreeStorage   string
	WorktreeRepoName  string
	WorktreeHostPath  string
	// RepoURL / RepoRef surface as --sets only for a mount-source runtime deploy.
	RepoURL            string
	RepoRef            string
	SSHDEnabled        bool
	MCPPort            int
	APIPort            int
	SSHPort            int
	ManagedCloud       bool
	CloudContextName   string
	CloudProvider      string
	CloudProviderAlias string
	CloudRegion        string
	CloudInstanceID    string
	UseHostCredentials bool
	OIDCAllowedIssuers string
	ContainerRegistry  string
	// InsecureRegistry is a plain-HTTP cluster registry the in-pod dind daemon must trust; empty for TLS registries.
	InsecureRegistry string
	ImageOverrides   map[string]string
	// ResolvedRuntimeImage is a display memo of the runtime image; helm never receives it.
	// Empty when the path cannot know the image; callers must not guess.
	ResolvedRuntimeImage string
	// PersistRuntimeImage is the bare component name written back to EnvConfig.RuntimeImage,
	// which future deploys read. Empty leaves the persisted field untouched.
	PersistRuntimeImage string
	// PersistRuntimeChart is the stated --runtime-chart written back to EnvConfig.RuntimeChart; empty leaves it.
	PersistRuntimeChart string
	// ImagePullSecrets mirrors EnvConfig.ImagePullSecrets; empty renders nothing.
	ImagePullSecrets []string
	// RegistryCredentialSecretName mirrors EnvConfig.RegistryCredentialSecretName; only the runtime chart consumes it.
	RegistryCredentialSecretName string
	// PlatformAliasSecretName mirrors EnvConfig.PlatformAliasSecretName; only the runtime chart consumes it.
	PlatformAliasSecretName string
	ResetDatabase           bool
	Idle                    configpkg.EnvironmentIdleConfig
	Claude                  configpkg.EnvironmentClaudeConfig
	// OpenRouter comes from root config, not EnvConfig: one catalog every env selects from. Nil renders nothing.
	OpenRouter *configpkg.OpenRouterConfig
	RuntimePod model.RuntimePodResources
	// RuntimeDindPod sizes the erun-dind sidecar; zero normalizes to DefaultRuntimeDindCPU/Memory.
	RuntimeDindPod model.RuntimePodResources
	// NamespaceQuota is a per-namespace ResourceQuota + LimitRange ceiling; zero applies none.
	NamespaceQuota model.NamespaceResourceQuota
	// Stopped re-renders replicas: 0 so an upgrade does not wake a stopped env; only `erun open` wakes.
	Stopped bool
	Version string
	// ChartVersion is the runtime chart's version when it differs from Version: a tenant-versioned
	// image still uses a chart that exists only at erun's versions.
	ChartVersion string
	// ChartCandidates lists the probed runtime chart coordinates so a failed pull names where it looked.
	ChartCandidates []string
	// RuntimeChartRegistry is where the runtime chart search resolved, which may be past the env's
	// own registry; it is memoized as the env's runtimeregistry. Empty when nothing was searched.
	RuntimeChartRegistry string
	Timeout              string
	Verbosity            int
	// Cloudflare*: CloudflareTokenRef is a secret-store handle, never the token, resolved at execution.
	CloudflareEnabled    bool
	CloudflareAccountID  string
	CloudflareSecretName string
	CloudflareTokenRef   string
	// RuntimeRegistry keeps in-pod runtime image-ref resolution from falling back to ghcr.io/sophium.
	RuntimeRegistry string
	// ContainerRegistries is injected because remote/runtime pods have the list only on the env config.
	ContainerRegistries configpkg.ContainerRegistries
	DisableBuildScript  bool
	// PlatformAccount binds the env's SA to cluster-admin for in-pod platform terraform.
	PlatformAccount bool
	// Platform is threaded to every chart as platform.* values for per-instance identity.
	Platform configpkg.PlatformConfig
	// MCPAuth* make the erun-mcp edge verify bearer tokens; empty keeps loopback-only pass-through.
	MCPAuthEnabled      bool
	MCPAuthPublicKeyPEM string
	MCPAuthIssuer       string
	MCPAuthAudience     string
	MCPAuthSecretName   string
	// MCPAuthPublicKeyPath is recorded so a later redeploy rethreads the same key.
	MCPAuthPublicKeyPath string
}

type HelmReleaseRecoveryParams struct {
	ReleaseName       string
	Namespace         string
	KubernetesContext string
	Verbosity         int
	Stdout            io.Writer
	Stderr            io.Writer
}

// DeploySpec is a pure helm-install plan by reference; deploy never builds, pushes, or publishes.
type DeploySpec struct {
	Target        configpkg.OpenResult
	DeployContext model.KubernetesDeployContext
	Deploy        HelmDeploySpec
	// SkipHelm suppresses the upgrade when every image was promoted from cache, so pods are not rolled.
	SkipHelm bool
}
