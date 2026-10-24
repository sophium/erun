package deploy

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

const (
	WorktreeStorageHost = "host"
	WorktreeStoragePVC  = "pvc"
	// WorktreeStorageNone is informational for runtime envs; the chart treats it as "skip worktree mount".
	WorktreeStorageNone = "none"
)

type DeployStore interface {
	configpkg.OpenStore
	ListTenantConfigs() ([]configpkg.TenantConfig, error)
	ListEnvConfigs(string) ([]configpkg.EnvConfig, error)
}

type (
	DeployContextResolverFunc       func() (shared.KubernetesDeployContext, error)
	KubernetesDeploymentCheckerFunc func(core.Context, KubernetesDeploymentCheckParams) (bool, error)
	HelmChartDeployerFunc           func(HelmDeployParams) error
	HelmReleaseRecovererFunc        func(HelmReleaseRecoveryParams) error
)

type deployKubernetesContextResolver interface {
	ResolveDeployKubernetesContext(environment, configured string) string
}

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
	RuntimePod configpkg.RuntimePodResources
	// RuntimeDindPod sizes the erun-dind sidecar; zero normalizes to DefaultRuntimeDindCPU/Memory.
	RuntimeDindPod configpkg.RuntimePodResources
	// NamespaceQuota is a per-namespace ResourceQuota + LimitRange ceiling; zero applies none.
	NamespaceQuota configpkg.NamespaceResourceQuota
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

type HelmReleasePendingOperationError struct {
	ReleaseName       string
	Namespace         string
	KubernetesContext string
	// Tenant and Environment address `erun doctor --clear-pending-helm`; empty names only the kubectl command.
	Tenant      string
	Environment string
	Message     string
	Err         error
}

type KubernetesDeploymentCheckParams struct {
	Name               string
	Namespace          string
	KubernetesContext  string
	ExpectedRepoPath   string
	ExpectedSSHD       *bool
	ExpectedMCPPort    int
	ExpectedSSHPort    int
	ExpectedRuntimePod configpkg.RuntimePodResources
}

type DeployTarget struct {
	Tenant          string
	Environment     string
	RepoPath        string
	VersionOverride string
	// Components, when non-empty, fully determines what deploys, overriding the saved set and plan.
	// Unknown names error at resolve.
	Components []string
	// Force re-runs the helm upgrade even when the release already matches the version.
	Force bool
	// RolloutTimeout overrides deploy.timeout and DefaultHelmDeploymentTimeout for this deploy only.
	RolloutTimeout string
	// NamespaceQuotaOverride overrides the env's namespace ceiling for this deploy only.
	NamespaceQuotaOverride configpkg.NamespaceResourceQuota
	// MCPAuthPublicKeyPath empty rethreads the recorded key, so auth survives a version bump.
	MCPAuthPublicKeyPath string
	// DisableMCPAuth is the only way to turn auth off, so no deploy downgrades an edge by omission.
	DisableMCPAuth bool
	// RuntimeImageOverride lets an env run another image before its own <tenant>-devops image exists.
	RuntimeImageOverride string
	// RuntimeChartOverride is an OCI chart reference, optionally versioned, stated by the operator.
	RuntimeChartOverride string
}

// DeploySpec is a pure helm-install plan by reference; deploy never builds, pushes, or publishes.
type DeploySpec struct {
	Target        configpkg.OpenResult
	DeployContext shared.KubernetesDeployContext
	Deploy        HelmDeploySpec
	// SkipHelm suppresses the upgrade when every image was promoted from cache, so pods are not rolled.
	SkipHelm bool
}

type EnvConfigSaver func(tenant string, config configpkg.EnvConfig) error

// PersistRuntimeVersionFromDeploySpecs writes the deployed runtime version and registry to the env
// config. A SkipHelm version was never pushed, so it heals to the release's running version instead.
func PersistRuntimeVersionFromDeploySpecs(ctx core.Context, specs []DeploySpec, save EnvConfigSaver, resolveDeployedVersion HelmReleaseVersionResolverFunc) error {
	if save == nil || ctx.DryRun || len(specs) == 0 {
		return nil
	}
	for _, spec := range specs {
		if !specDeploysRuntimeChart(spec) {
			continue
		}
		if spec.SkipHelm {
			// With a separately stated chart, the release appVersion is the chart's, not the runtime version.
			if strings.TrimSpace(spec.Deploy.ChartVersion) != "" {
				ctx.Trace("persist runtime version: chart " + spec.Deploy.ChartPath + " is on its own version line; leaving the recorded runtime version untouched")
				return nil
			}
			version := resolveRunningRuntimeVersion(ctx, spec, resolveDeployedVersion)
			if version == "" {
				return nil
			}
			return persistRuntimeVersionIfChanged(spec, version, save)
		}
		version := strings.TrimSpace(spec.Deploy.Version)
		if version == "" {
			continue
		}
		return persistRuntimeVersionIfChanged(spec, version, save)
	}
	return nil
}

// An empty result means unknown; the caller then leaves the persisted version alone.
func resolveRunningRuntimeVersion(ctx core.Context, spec DeploySpec, resolveDeployedVersion HelmReleaseVersionResolverFunc) string {
	if resolveDeployedVersion == nil {
		return ""
	}
	version, err := resolveDeployedVersion(ctx, spec.Deploy.ReleaseName, spec.Deploy.Namespace, spec.Deploy.KubernetesContext)
	if err != nil {
		ctx.Trace("persist runtime version: reading deployed version for " + spec.Deploy.ReleaseName + " failed: " + err.Error())
		return ""
	}
	return strings.TrimSpace(version)
}

// RuntimeRegistryForDeploySpec returns the registry a deploy memoizes. A runtime chart search only
// fills or confirms the memo, never overriding `erun init --runtime-registry`.
func RuntimeRegistryForDeploySpec(spec DeploySpec) string {
	chartRegistry := strings.TrimSpace(spec.Deploy.RuntimeChartRegistry)
	if chartRegistry == "" {
		return strings.TrimSpace(spec.Deploy.ContainerRegistry)
	}
	if recorded := strings.TrimSpace(spec.Target.EnvConfig.RuntimeRegistry); recorded != "" {
		return recorded
	}
	return chartRegistry
}

// An empty override never blocks the short-circuit: it means this deploy named none.
func runtimeDeployMemoUnchanged(envConfig configpkg.EnvConfig, version, registry, mcpAuthKeyPath, runningImage, runtimeImage, runtimeChart string) bool {
	return strings.TrimSpace(envConfig.RuntimeVersion) == version &&
		strings.TrimSpace(envConfig.RuntimeRegistry) == registry &&
		strings.TrimSpace(envConfig.MCPAuthPublicKeyPath) == mcpAuthKeyPath &&
		strings.TrimSpace(envConfig.RuntimeRunningImage) == runningImage &&
		(runtimeImage == "" || strings.TrimSpace(envConfig.RuntimeImage) == runtimeImage) &&
		(runtimeChart == "" || strings.TrimSpace(envConfig.RuntimeChart) == runtimeChart)
}

func persistRuntimeVersionIfChanged(spec DeploySpec, version string, save EnvConfigSaver) error {
	registry := RuntimeRegistryForDeploySpec(spec)
	// The opt-out's clear is recorded only once the unauthenticated release rolled out.
	mcpAuthKeyPath := strings.TrimSpace(spec.Deploy.MCPAuthPublicKeyPath)
	runningImage := strings.TrimSpace(spec.Deploy.ResolvedRuntimeImage)
	runtimeImage := strings.TrimSpace(spec.Deploy.PersistRuntimeImage)
	runtimeChart := strings.TrimSpace(spec.Deploy.PersistRuntimeChart)
	envConfig := spec.Target.EnvConfig
	if runtimeDeployMemoUnchanged(envConfig, version, registry, mcpAuthKeyPath, runningImage, runtimeImage, runtimeChart) {
		return nil
	}
	// The pre-rollout step wrote PlatformAliasSecretName during the rollout; saving the snapshot
	// wholesale would drop it, and with it the chart's mount on the next deploy.
	if strings.TrimSpace(envConfig.PlatformAliasSecretName) == "" {
		if current, _, err := configpkg.LoadEnvConfig(spec.Target.Tenant, spec.Target.Environment); err == nil {
			envConfig.PlatformAliasSecretName = strings.TrimSpace(current.PlatformAliasSecretName)
		}
	}
	envConfig.RuntimeVersion = version
	envConfig.RuntimeRegistry = registry
	envConfig.MCPAuthPublicKeyPath = mcpAuthKeyPath
	// Healed unconditionally: a stale memo from another resolution path is worse than none.
	envConfig.RuntimeRunningImage = runningImage
	// RuntimeImage is operative, so an empty PersistRuntimeImage must never clobber it.
	if runtimeImage != "" {
		envConfig.RuntimeImage = runtimeImage
	}
	if runtimeChart != "" {
		envConfig.RuntimeChart = runtimeChart
	}
	if err := save(spec.Target.Tenant, envConfig); err != nil {
		return fmt.Errorf("persist runtime version after deploy: %w", err)
	}
	return nil
}

func (d HelmDeploySpec) resolvedChartVersion() string {
	if version := strings.TrimSpace(d.ChartVersion); version != "" {
		return version
	}
	return strings.TrimSpace(d.Version)
}

// applyRuntimeChartOverride installs the operator-named runtime chart. erun publishes the chart on
// its own line, so a tenant-versioned image may have no chart at its version.
func applyRuntimeChartOverride(ctx core.Context, target DeployTarget, spec *DeploySpec) {
	override := strings.TrimSpace(target.RuntimeChartOverride)
	if override == "" || spec == nil {
		return
	}
	if spec.Deploy.ReleaseName != configpkg.RuntimeReleaseName(spec.Target.Tenant) {
		return
	}
	reference, version := SplitChartReferenceVersion(override)
	spec.DeployContext.ChartPath = reference
	spec.Deploy.ChartPath = reference
	spec.Deploy.ChartVersion = version
	spec.Deploy.PersistRuntimeChart = override
	// The subchart key belongs to the named chart: helm silently ignores values for a missing subchart,
	// so a stale key would drop the image override and required values.
	spec.Deploy.SubchartKey = publishedUmbrellaSubchartKey(spec.Target.Tenant, ChartName(reference))
	if version == "" {
		ctx.Trace("deploy: runtime chart override " + reference + " at the deploy version")
		return
	}
	ctx.Trace("deploy: runtime chart override " + reference + " version " + version)
}

// ChartName is a reference's last path segment, as helm reads it.
func ChartName(reference string) string {
	return reference[strings.LastIndex(reference, "/")+1:]
}

// SplitChartReference separates a chart reference into repository and optional version.
func SplitChartReference(raw string) (reference, version string) {
	return SplitChartReferenceVersion(raw)
}

// ChartNameFromReference is ChartName for callers outside this package.
func ChartNameFromReference(reference string) string {
	return ChartName(reference)
}

// SplitChartReferenceVersion inspects only the last path segment, so a registry port is not a version,
// and adds oci:// when absent.
func SplitChartReferenceVersion(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "oci://" + raw
	}
	cut := strings.LastIndex(raw, "/")
	if cut < 0 {
		return raw, ""
	}
	last := raw[cut+1:]
	sep := strings.LastIndex(last, ":")
	if sep <= 0 {
		return raw, ""
	}
	return raw[:cut+1] + last[:sep], last[sep+1:]
}

// HelmReleaseVersionResolverFunc reads a deployed release's appVersion.
type HelmReleaseVersionResolverFunc func(ctx core.Context, releaseName, namespace, kubernetesContext string) (string, error)

// ResolveDeployedHelmReleaseVersion returns a release's appVersion, or "" with nil error when unavailable.
func ResolveDeployedHelmReleaseVersion(ctx core.Context, releaseName, namespace, kubernetesContext string) (string, error) {
	releaseName = strings.TrimSpace(releaseName)
	namespace = strings.TrimSpace(namespace)
	if releaseName == "" || namespace == "" {
		return "", nil
	}
	args := []string{"get", "metadata", releaseName, "--namespace", namespace, "-o", "json"}
	if kubernetesContext = strings.TrimSpace(kubernetesContext); kubernetesContext != "" {
		args = append(args, "--kube-context", kubernetesContext)
	}
	return helmReleaseAppVersion(args), nil
}

func helmReleaseAppVersion(args []string) string {
	output, err := core.Command("helm", args...).Output()
	if err != nil {
		return ""
	}
	var metadata struct {
		AppVersion string `json:"appVersion"`
	}
	if err := json.Unmarshal(output, &metadata); err != nil {
		return ""
	}
	return strings.TrimSpace(metadata.AppVersion)
}

func specDeploysRuntimeChart(spec DeploySpec) bool {
	tenant := strings.TrimSpace(spec.Target.Tenant)
	return tenant != "" && strings.TrimSpace(spec.Deploy.ReleaseName) == configpkg.RuntimeReleaseName(tenant)
}

// RunDeploySpecs owns the step-timing root for `erun deploy` and runs every spec; `build --deploy`
// calls RunDeploySpec under the build root instead.
func RunDeploySpecs(ctx core.Context, executions []DeploySpec, deploy HelmChartDeployerFunc) error {
	if len(executions) == 0 {
		return nil
	}
	if !ctx.DryRun {
		ctx.Timing = core.NewStepTiming("deploy", nil)
	}
	err := runDeploySpecsPlan(ctx, executions, deploy)
	if root := ctx.Timing; root != nil {
		root.Finish(err)
		core.ReportStepTiming(ctx, "deploy", root)
	}
	return err
}
