package deploy

import (
	"encoding/json"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
)

const (
	WorktreeStorageHost = "host"
	WorktreeStoragePVC  = "pvc"
	// WorktreeStorageNone is informational for runtime envs; the chart treats it as "skip worktree mount".
	WorktreeStorageNone = "none"
)

type deployKubernetesContextResolver interface {
	ResolveDeployKubernetesContext(environment, configured string) string
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

// PersistRuntimeVersionFromDeploySpecs writes the deployed runtime version and registry to the env
// config. A SkipHelm version was never pushed, so it heals to the release's running version instead.
func PersistRuntimeVersionFromDeploySpecs(ctx core.Context, specs []seam.DeploySpec, save seam.EnvConfigSaver, resolveDeployedVersion seam.HelmReleaseVersionResolverFunc) error {
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
func resolveRunningRuntimeVersion(ctx core.Context, spec seam.DeploySpec, resolveDeployedVersion seam.HelmReleaseVersionResolverFunc) string {
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
func RuntimeRegistryForDeploySpec(spec seam.DeploySpec) string {
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

func persistRuntimeVersionIfChanged(spec seam.DeploySpec, version string, save seam.EnvConfigSaver) error {
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

func resolvedChartVersion(d seam.HelmDeploySpec) string {
	if version := strings.TrimSpace(d.ChartVersion); version != "" {
		return version
	}
	return strings.TrimSpace(d.Version)
}

// applyRuntimeChartOverride installs the operator-named runtime chart. erun publishes the chart on
// its own line, so a tenant-versioned image may have no chart at its version.
func applyRuntimeChartOverride(ctx core.Context, target model.DeployTarget, spec *seam.DeploySpec) {
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

func specDeploysRuntimeChart(spec seam.DeploySpec) bool {
	tenant := strings.TrimSpace(spec.Target.Tenant)
	return tenant != "" && strings.TrimSpace(spec.Deploy.ReleaseName) == configpkg.RuntimeReleaseName(tenant)
}

// RunDeploySpecs owns the step-timing root for `erun deploy` and runs every spec; `build --deploy`
// calls RunDeploySpec under the build root instead.
func RunDeploySpecs(ctx core.Context, executions []seam.DeploySpec, deploy seam.HelmChartDeployerFunc) error {
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
