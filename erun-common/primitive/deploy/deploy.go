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
	// WorktreeStorageNone is reported for runtime envs that have no worktree.
	// The erun-devops chart is not deployed for runtime envs, so this value
	// is informational; chart-side compat treats it as "skip worktree mount".
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

// HelmDeployParams is the execution view of a resolved deploy: the full
// HelmDeploySpec (the single source of truth for the helm command) plus the
// output writers the rollout wires. It embeds the spec rather than copying a
// hand-maintained field list — a copy list silently dropped
// Cloudflare/MCP-auth/registry/platform values from real deploys while the
// traced command (built from the full spec) still showed them.
type HelmDeployParams struct {
	HelmDeploySpec
	Stdout io.Writer
	Stderr io.Writer
}

type HelmDeploySpec struct {
	ReleaseName    string
	ChartPath      string
	ValuesFilePath string
	// PulledValuesFilePath is the local path to a by-reference umbrella's bundled
	// values.<env>.yaml, extracted from the published chart by a deploy-time
	// `helm pull --untar`. helm applies only a chart's default values.yaml, so a
	// tenant umbrella's per-env subchart values (pod-shape and overrides authored
	// under the subchart key) never reach a by-reference deploy otherwise. It is
	// -f'd before ValuesFilePath so an operator's config-dir overlay still wins.
	// Empty for local charts (their values.<env>.yaml is ValuesFilePath) and for a
	// canonical chart installed directly. Set by RunHelmDeploy, not at resolve.
	PulledValuesFilePath string
	// SubchartKey is set when the chart is an umbrella wrapping a canonical
	// erun-<base> chart as a subchart — the repo-local runtime umbrella (the
	// erun-devops dependency's alias/name) or a tenant's published <tenant>-<base>
	// umbrella deployed by reference (the wrapped erun-<base>). helm does not pass
	// top-level --set values into subchart scope, so command() prefixes every
	// --set key with this key. Empty for a canonical chart installed directly and
	// a forked top-level chart — those keep byte-identical top-level --sets.
	SubchartKey       string
	Tenant            string
	Environment       string
	Namespace         string
	KubernetesContext string
	WorktreeStorage   string
	WorktreeRepoName  string
	WorktreeHostPath  string
	// RepoURL / RepoRef mirror the HelmDeployParams fields of the same name; see
	// their doc comment there. They surface as chart repoUrl/repoRef --sets only
	// for a mount-source runtime deploy.
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
	// InsecureRegistry, when set, is a plain-HTTP cluster registry host the in-pod
	// dind daemon must trust so an in-pod build can push to it. Empty for hosted
	// (TLS) registries, so those envs render nothing.
	InsecureRegistry string
	ImageOverrides   map[string]string
	// ResolvedRuntimeImage is the runtime pod's image as this deploy resolved
	// it for the erun-devops component -- the operator's override, the
	// tenant's own default, or the chart's stock default -- recorded here for
	// PersistRuntimeVersionFromDeploySpecs to heal into
	// EnvConfig.RuntimeRunningImage. It is a display memo only: helm only ever
	// receives ImageOverrides, never this field, so it cannot change what a
	// deploy installs. Empty when the resolution ran on a path that does not
	// know the image (a repo-local runtime chart's own values decide it) --
	// callers must not guess a value for that case.
	ResolvedRuntimeImage string
	// PersistRuntimeImage is the bare component name (no registry, no tag)
	// PersistRuntimeVersionFromDeploySpecs writes back to
	// EnvConfig.RuntimeImage -- the operative field a future deploy reads back
	// to pick the pod's image, unlike ResolvedRuntimeImage above which is
	// display-only. Empty leaves the persisted field untouched: either an
	// operator's own override already names it (nothing to heal), or this
	// deploy's resolution has no line of its own to default to (the erun
	// product's own environments).
	PersistRuntimeImage string
	// PersistRuntimeChart is the operator-stated chart coordinate (as
	// --runtime-chart names it) that PersistRuntimeVersionFromDeploySpecs writes
	// back to EnvConfig.RuntimeChart -- without it, an operator's own
	// --runtime-chart override would install correctly this one time and then be
	// forgotten, the same shape RuntimeImage/PersistRuntimeImage above exists to
	// prevent, but for the chart instead of the image. Empty leaves the
	// persisted field untouched: no override was named on this deploy.
	PersistRuntimeChart string
	// ImagePullSecrets names dockerconfigjson secrets the runtime pod pulls with,
	// threaded to the chart as imagePullSecrets[i].name. Empty renders nothing, so
	// public-image envs are byte-for-byte unchanged. Mirrors EnvConfig.ImagePullSecrets.
	ImagePullSecrets []string
	// RegistryCredentialSecretName names the dockerconfigjson Secret `erun init`
	// minted from the host's own resolved ghcr.io credential, threaded to
	// the runtime chart as registryCredentialSecretName so it can mount it into
	// the pod's docker config. Empty renders nothing, so an env init never
	// provisioned one for is byte-for-byte unchanged. Mirrors
	// EnvConfig.RegistryCredentialSecretName; only the runtime chart consumes it.
	RegistryCredentialSecretName string
	// PlatformAliasSecretName names the Secret `erun init` minted from the
	// invoking host's own signed-in erun platform cloud provider alias, threaded
	// to the runtime chart as platformAliasSecretName so it can mount it where
	// the pod's `erun` reads its cloud config. Empty renders nothing, so an env
	// init found no host alias for is byte-for-byte unchanged. Mirrors
	// EnvConfig.PlatformAliasSecretName; only the runtime chart consumes it.
	PlatformAliasSecretName string
	ResetDatabase           bool
	Idle                    configpkg.EnvironmentIdleConfig
	Claude                  configpkg.EnvironmentClaudeConfig
	// OpenRouter is the erun-level gateway catalog stamped from root config (see
	// openrouter.go). It is deliberately not read from EnvConfig: the catalog is
	// one list the operator maintains and every environment selects from it. Nil
	// renders no gateway values at all, so an unconfigured install deploys
	// byte-for-byte as before.
	OpenRouter *configpkg.OpenRouterConfig
	RuntimePod configpkg.RuntimePodResources
	// RuntimeDindPod sizes the erun-dind sidecar's own CPU/memory limits,
	// mirroring RuntimePod but for the sidecar container that runs the actual
	// docker builds. Zero normalizes to DefaultRuntimeDindCPU/Memory, so an env
	// that never sized it deploys byte-for-byte as before.
	RuntimeDindPod configpkg.RuntimePodResources
	// NamespaceQuota is a hard per-namespace ceiling applied via a Kubernetes
	// ResourceQuota + LimitRange (kubernetes_resource_quota.go), distinct from
	// RuntimePod which only sizes the runtime container itself. Zero applies no
	// ResourceQuota/LimitRange, so an env with no cap configured is unaffected.
	NamespaceQuota configpkg.NamespaceResourceQuota
	// Stopped mirrors EnvConfig.Stopped so a helm upgrade of a stopped
	// environment re-renders replicas: 0 instead of restarting the pod the
	// operator deliberately scaled away. deploy never decides run/stop itself —
	// it only reconciles the recorded intent, and `erun open` is what wakes.
	Stopped bool
	Version string
	// ChartVersion is the runtime chart's own version, set only when it differs
	// from Version. The two coincide for a tenant riding erun's release line and
	// must not when the runtime image is versioned on the tenant's own line: the
	// chart is erun's artifact and exists only at erun's versions, so a deploy
	// that pinned both to one number could not name that pair. Empty keeps the
	// chart on Version.
	ChartVersion string
	// ChartCandidates records the coordinates the runtime chart ladder probed,
	// in order, so a failed chart pull names where the deploy looked instead of
	// blaming the version. Empty for a stated chart and for component charts,
	// neither of which is searched for.
	ChartCandidates []string
	// RuntimeChartRegistry is the registry the runtime chart search resolved
	// from, which is not always the registry it started at — erun publishes its
	// platform chart only where it releases, so the search can land a rung past
	// the env's own. It is what the post-deploy persist memoizes as the env's
	// runtimeregistry, so the memo names where erun's artifacts really are.
	// Empty for every deploy that did not search: a repo-local chart, a chart the
	// env states, and component charts.
	RuntimeChartRegistry string
	Timeout              string
	Verbosity            int
	// Cloudflare* deliver a delegated Cloudflare token to the runtime pod.
	// CloudflareTokenRef is a handle into the secret store, never the token
	// itself, resolved at execution time.
	CloudflareEnabled    bool
	CloudflareAccountID  string
	CloudflareSecretName string
	CloudflareTokenRef   string
	// RuntimeRegistry is the env's runtime image-ref / runtime-version registry,
	// distinct from ContainerRegistry (the DEPLOY-marked registry the cluster
	// pulls from). Injected so in-pod runtime image-ref resolution does not fall
	// back to ghcr.io/sophium.
	RuntimeRegistry string
	// ContainerRegistries is the env's full marked registry list, injected so
	// in-pod build/push role resolution works on remote/runtime pods whose list
	// lives only on the env config.
	ContainerRegistries configpkg.ContainerRegistries
	// DisableBuildScript mirrors EnvConfig.DisableBuildScript so a remote-agent
	// pod's in-pod build honours the operator's build.sh-discovery choice.
	DisableBuildScript bool
	// PlatformAccount mirrors EnvConfig.PlatformAccount: when set, the runtime
	// chart binds this env's SA to cluster-admin so in-pod platform terraform
	// and component installs can manage cluster-scoped resources.
	PlatformAccount bool
	// Platform is the resolved per-instance platform config. Zero for
	// non-platform projects; when set, deploy threads it to every chart as
	// platform.* values so the platform singletons can resolve their
	// per-instance identity — PowerDNS its authoritative zone, Zitadel the auth
	// host it issues tokens for.
	Platform configpkg.PlatformConfig
	// MCPAuth* require the per-env erun-mcp edge to authenticate bearer tokens
	// against a trusted public key, delivered out-of-band as a Secret like the
	// Cloudflare token. Empty leaves the edge in legacy loopback-only
	// pass-through, so non-desktop deploys are byte-for-byte unchanged.
	MCPAuthEnabled      bool
	MCPAuthPublicKeyPEM string
	MCPAuthIssuer       string
	MCPAuthAudience     string
	MCPAuthSecretName   string
	// MCPAuthPublicKeyPath is the key's on-host location, recorded on the env
	// after the deploy so a later redeploy rethreads the same key instead of
	// dropping the edge back to unauthenticated.
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
	// Tenant and Environment name the erun target the recovery is addressed to
	// (`erun doctor --clear-pending-helm <tenant> <environment>`), which the
	// helm-level coordinates above cannot express. Empty on a caller that has
	// no erun target, in which case the error names the kubectl command only.
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
	// Components is the explicit, one-shot deploy selection for this run. When
	// non-empty it fully determines what deploys (opt-in-only), overriding the
	// env's saved deploy.components and the repo plan; the runtime deploys only
	// if named, and a name matching no chart or runtime alias errors at resolve.
	// Empty means no explicit selection: deploy falls back to the saved set, then
	// the plan, then the runtime chart alone.
	Components []string
	// Force re-runs the helm upgrade even when the deployed release already
	// matches the requested version (no-op rollouts are otherwise skipped).
	Force bool
	// RolloutTimeout overrides the helm rollout timeout for this deploy only,
	// taking precedence over the env's deploy.timeout and
	// DefaultHelmDeploymentTimeout. Empty leaves the resolved per-env/default
	// value untouched.
	RolloutTimeout string
	// NamespaceQuotaOverride overrides the env's saved namespace resource
	// ceiling for this deploy only (see NamespaceResourceQuota). Zero leaves the
	// env's own EnvConfig.NamespaceQuota untouched.
	NamespaceQuotaOverride configpkg.NamespaceResourceQuota
	// MCPAuthPublicKeyPath, when set, points at a PEM public key the runtime
	// chart trusts so the per-env erun-mcp edge requires a bearer token signed by
	// it. Empty rethreads the key the env recorded on its last authenticated
	// deploy, so authentication survives a plain version bump.
	MCPAuthPublicKeyPath string
	// DisableMCPAuth is the explicit opt-out: it deploys the MCP edge
	// unauthenticated (loopback-only) and clears the env's recorded key. Required
	// to turn auth off, so no deploy can downgrade a reachable edge by omission.
	DisableMCPAuth bool
	// RuntimeImageOverride lets an operator stand up an env on the shared ERun
	// base image (or any external image) before the env's own <tenant>-devops
	// image exists, then switch to the tenant image once it is built. Empty
	// leaves runtime-chart resolution untouched.
	RuntimeImageOverride string
	// RuntimeChartOverride names the runtime chart to install, as an OCI
	// reference that may carry its own version. It makes the chart a coordinate
	// the operator states rather than one derived from the deploy version and
	// the registry a previous deploy happened to record.
	RuntimeChartOverride string
}

// DeploySpec is a pure helm-install plan: it installs the image and chart
// already published at a version, by reference. deploy does not build, push, or
// publish — those are the build and push primitives, composed above deploy by an
// orchestrator (the build --deploy shortcut, erun open --deploy, or the UI).
type DeploySpec struct {
	Target        configpkg.OpenResult
	DeployContext shared.KubernetesDeployContext
	Deploy        HelmDeploySpec
	// SkipHelm lets an orchestrator suppress the helm upgrade when every
	// image this chart references was promoted from cache (no rebuild), so
	// unchanged pods are not rolled. A pure deploy leaves it false; the
	// build orchestration sets it from its build results.
	SkipHelm bool
}

// EnvConfigSaver writes an updated env config to disk.
type EnvConfigSaver func(tenant string, config configpkg.EnvConfig) error

// PersistRuntimeVersionFromDeploySpecs writes the just-deployed runtime chart's
// version and source registry back into the env config so downstream readers
// (the desktop runtime dialog, `erun list`, and a bare `erun open`) reflect the
// deployed state rather than a stale persisted version. The registry is recorded
// as provenance so a later reopen addresses the same image even if the user
// edits the project's container registry.
//
// A runtime chart whose upgrade was skipped (SkipHelm: every image promoted from
// cache, nothing pushed) carries a freshly minted version that was never pushed;
// persisting it would point the env config at a phantom version the deploy
// picker can never offer. Instead, heal RuntimeVersion to the version the release
// is actually running, which is guaranteed pushed, and leave it untouched when
// that cannot be read.
func PersistRuntimeVersionFromDeploySpecs(ctx core.Context, specs []DeploySpec, save EnvConfigSaver, resolveDeployedVersion HelmReleaseVersionResolverFunc) error {
	if save == nil || ctx.DryRun || len(specs) == 0 {
		return nil
	}
	for _, spec := range specs {
		if !specDeploysRuntimeChart(spec) {
			continue
		}
		if spec.SkipHelm {
			// The release's appVersion is the chart's version, which is only the
			// runtime version while the two ride one line. When the env states its
			// chart separately it does not, so healing from it would record the
			// chart's number as the env's runtime version -- the exact confusion
			// naming the coordinates separately exists to end. Leave the recorded
			// version alone instead.
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

// resolveRunningRuntimeVersion returns the version the runtime release is
// actually running. An empty string means "could not determine" — the caller
// then leaves the persisted version untouched rather than record the
// never-pushed mint.
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

// RuntimeRegistryForDeploySpec answers what a deploy records as the env's
// runtimeregistry — the memo of where this environment resolves erun's own
// artifacts from. A by-reference runtime deploy searches for erun's chart, so it
// records where the search resolved rather than where it started; but it only
// fills the memo in or confirms it, never replaces a value that disagrees:
// `erun init --runtime-registry` is how an operator redirects that search, and a
// deploy that resolved elsewhere would otherwise take the choice back on the
// next successful rollout. Every other deploy records the registry it pulled
// from, which is the provenance a `--current` redeploy re-addresses.
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

// runtimeDeployMemoUnchanged reports whether envConfig already carries every
// value this deploy would write, so persistRuntimeVersionIfChanged can skip
// the save. An empty override (runtimeImage/runtimeChart) never blocks the
// short-circuit: it means this deploy named none, not that a recorded one
// should read as a mismatch.
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
	// The MCP-auth key path mirrors the deploy verbatim. An enabling deploy
	// already recorded it where helm applied it, so what this write adds is the
	// explicit opt-out's clear — and forgetting the key is only true once the
	// unauthenticated release actually rolled out.
	mcpAuthKeyPath := strings.TrimSpace(spec.Deploy.MCPAuthPublicKeyPath)
	runningImage := strings.TrimSpace(spec.Deploy.ResolvedRuntimeImage)
	runtimeImage := strings.TrimSpace(spec.Deploy.PersistRuntimeImage)
	runtimeChart := strings.TrimSpace(spec.Deploy.PersistRuntimeChart)
	envConfig := spec.Target.EnvConfig
	if runtimeDeployMemoUnchanged(envConfig, version, registry, mcpAuthKeyPath, runningImage, runtimeImage, runtimeChart) {
		return nil
	}
	// spec.Target.EnvConfig is the environment as this deploy resolved it, before
	// its rollout -- and the pre-rollout step writes the env config during that
	// rollout. Saving the snapshot wholesale therefore discards those writes;
	// here that is the platform-alias Secret name reconcilePlatformAliasSecret
	// has just recorded, which only ever reaches the environment from the deploy
	// itself (a retrofit discovers the name at deploy time, so unlike the MCP
	// auth key above it cannot be rethreaded from the snapshot). The next deploy
	// reads that field back to keep the chart's mount on an environment whose
	// host has no alias left to give, so losing it drops the mount silently.
	if strings.TrimSpace(envConfig.PlatformAliasSecretName) == "" {
		if current, _, err := configpkg.LoadEnvConfig(spec.Target.Tenant, spec.Target.Environment); err == nil {
			envConfig.PlatformAliasSecretName = strings.TrimSpace(current.PlatformAliasSecretName)
		}
	}
	envConfig.RuntimeVersion = version
	envConfig.RuntimeRegistry = registry
	envConfig.MCPAuthPublicKeyPath = mcpAuthKeyPath
	// Healed unconditionally, including back to "" when this deploy's
	// resolution path could not know the image (a repo-local runtime chart):
	// a stale memo from a previous, different resolution path is worse than
	// none, since `erun list` would otherwise show a line that no longer
	// reflects what is actually running.
	envConfig.RuntimeRunningImage = runningImage
	// Unlike RuntimeRunningImage above, RuntimeImage is the operative field a
	// future deploy reads back to pick the pod's image, so an empty
	// PersistRuntimeImage here must never clobber it -- see
	// resolveDeployRuntimeImage's doc comment for what empty means.
	if runtimeImage != "" {
		envConfig.RuntimeImage = runtimeImage
	}
	// Same reasoning as RuntimeImage above: an empty PersistRuntimeChart means
	// this deploy named no chart override, not that the recorded one should
	// clear.
	if runtimeChart != "" {
		envConfig.RuntimeChart = runtimeChart
	}
	if err := save(spec.Target.Tenant, envConfig); err != nil {
		return fmt.Errorf("persist runtime version after deploy: %w", err)
	}
	return nil
}

// resolvedChartVersion is the version the runtime chart is pulled at: its own
// when the chart and the deploy are on different release lines, otherwise the
// deploy version.
func (d HelmDeploySpec) resolvedChartVersion() string {
	if version := strings.TrimSpace(d.ChartVersion); version != "" {
		return version
	}
	return strings.TrimSpace(d.Version)
}

// applyRuntimeChartOverride installs the runtime chart the operator named,
// instead of the one derived from the deploy version and the registry a previous
// deploy recorded.
//
// The chart and the runtime image are separate artifacts in separate registries,
// and a tenant that versions its image on its own release line has no chart at
// that version -- erun publishes the runtime chart on erun's line. Deriving both
// from one version can therefore name a pair that does not exist, and the
// operator had no way to say otherwise.
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
	// Record the coordinate exactly as stated so a future plain deploy or open
	// reads back the chart this one installed, instead of re-deriving it from
	// the version and losing the operator's override.
	spec.Deploy.PersistRuntimeChart = override
	// The value scope belongs to the chart, so it is recomputed from the named
	// one rather than left as the resolved chart's. Keeping the old key would
	// nest every runtime --set under a subchart the named chart may not wrap (or
	// drop the nesting one it does), and helm ignores values addressed to a
	// subchart that is not there -- so the image override and the required
	// tenant/environment values would land nowhere and the pod would come up
	// wrong instead of failing loudly.
	spec.Deploy.SubchartKey = publishedUmbrellaSubchartKey(spec.Target.Tenant, ChartName(reference))
	if version == "" {
		ctx.Trace("deploy: runtime chart override " + reference + " at the deploy version")
		return
	}
	ctx.Trace("deploy: runtime chart override " + reference + " version " + version)
}

// ChartName is the chart name a reference addresses -- its last
// path segment, the same reading helm gives it.
func ChartName(reference string) string {
	return reference[strings.LastIndex(reference, "/")+1:]
}

// SplitChartReference separates a stated chart reference into the repository and
// the version it may carry, so every surface reads one the same way -- the deploy
// that installs it and the desktop that shows what will be installed.
func SplitChartReference(raw string) (reference, version string) {
	return SplitChartReferenceVersion(raw)
}

// ChartNameFromReference is the chart name a reference addresses, for a caller
// outside this package that needs to name the chart a deploy will install.
func ChartNameFromReference(reference string) string {
	return ChartName(reference)
}

// SplitChartReferenceVersion separates a chart reference from the version it may
// carry. Only the last path segment is inspected, so a registry port is not read
// as a version, and an oci:// scheme is added when absent so the reference is
// unambiguous to helm.
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

// HelmReleaseVersionResolverFunc reads the appVersion of a deployed helm release
// — the runtime version the cluster is actually running — so a cached (SkipHelm)
// deploy can heal EnvConfig.RuntimeVersion to a real, pushed version instead of
// leaving a stale or phantom value.
type HelmReleaseVersionResolverFunc func(ctx core.Context, releaseName, namespace, kubernetesContext string) (string, error)

// ResolveDeployedHelmReleaseVersion returns the appVersion of the named helm
// release (the runtime version the cluster is actually running). It returns an
// empty string with a nil error when the release is absent or helm cannot be
// queried, so reading the version never blocks a deploy or open.
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

// helmReleaseAppVersion runs `helm get metadata` and returns the release's
// appVersion, or "" when helm fails or prints something unparseable.
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

// RunDeploySpecs starts the step-timing root for the whole `erun deploy`
// invocation (reported as a duration-ordered table plus a JSON record when it
// finishes, on success and on failure), then runs every resolved spec. It is
// always the outermost deploy entrypoint the CLI/MCP transports call, so it
// owns the root outright — `build --deploy` reaches RunDeploySpec per spec
// directly instead, attaching under the build root that already exists.
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
