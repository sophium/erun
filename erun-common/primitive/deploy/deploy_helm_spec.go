package deploy

import (
	"fmt"
	"io"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/helm"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func newHelmDeploySpec(target configpkg.OpenResult, deployContext shared.KubernetesDeployContext, versionOverride string) (HelmDeploySpec, error) {
	valuesFilePath, err := resolveKubernetesDeployValuesFile(deployContext.ChartPath, target.Environment)
	if err != nil {
		return HelmDeploySpec{}, err
	}
	return newHelmDeploySpecWithValues(target, deployContext, versionOverride, valuesFilePath)
}

// newHelmDeploySpecWithValues is newHelmDeploySpec for callers that resolve
// the values overlay themselves — a published OCI chart has no local chart
// directory to look in, so it passes an empty path.
func newHelmDeploySpecWithValues(target configpkg.OpenResult, deployContext shared.KubernetesDeployContext, versionOverride, valuesFilePath string) (HelmDeploySpec, error) {
	version := strings.TrimSpace(versionOverride)
	ports := configpkg.LocalPortsForResult(target)

	// The full marked registry list the pod acts on (remote-agent build/push;
	// runtime deploy). deployTargetContainerRegistries validates markers, so a
	// bad list surfaces here at deploy time rather than silently in-pod.
	containerRegistries, err := configpkg.DeployTargetContainerRegistries(target)
	if err != nil {
		return HelmDeploySpec{}, err
	}

	// Per-env rollout timeout (config > default). A flag/MCP override, when
	// present, is applied on top of this by the public resolve entrypoints.
	rolloutTimeout, err := target.EnvConfig.Deploy.Resolve()
	if err != nil {
		return HelmDeploySpec{}, err
	}

	// A repo-local runtime umbrella wraps the published erun-devops chart as a
	// subchart; helm won't pass the runtime --sets into subchart scope, so record
	// the subchart key and let command() prefix every runtime value with it. A
	// by-reference umbrella (published OCI chart) has no local Chart.yaml to read,
	// so its key is set by the published-spec resolvers instead.
	subchartKey := ""
	if deployContextOwnsRuntimeChart(deployContext, target.Tenant) && !isOCIChartReference(deployContext.ChartPath) {
		subchartKey, err = helm.HelmChartRuntimeSubchartKey(deployContext.ChartPath)
		if err != nil {
			return HelmDeploySpec{}, err
		}
	}

	// A context-resolved cluster registry supplies the concrete in-cluster pull
	// host; a plain registry keeps the on-disk DEPLOY entry, so non-cluster envs
	// render identically.
	containerRegistry := resolveProjectContainerRegistry(target.RepoPath, target.Environment)
	if strings.TrimSpace(target.ClusterPullRegistry) != "" {
		containerRegistry = target.ClusterPullRegistry
	}
	insecureRegistry := ""
	if target.ClusterRegistryInsecure {
		insecureRegistry = strings.TrimSpace(target.ClusterPullRegistry)
	}

	return HelmDeploySpec{
		ReleaseName:         deployContext.ComponentName,
		ChartPath:           deployContext.ChartPath,
		ValuesFilePath:      valuesFilePath,
		SubchartKey:         subchartKey,
		Tenant:              target.Tenant,
		Environment:         target.Environment,
		Namespace:           configpkg.KubernetesNamespaceName(target.Tenant, target.Environment),
		KubernetesContext:   target.EnvConfig.KubernetesContext,
		WorktreeStorage:     resolveWorktreeStorage(target),
		WorktreeRepoName:    resolveWorktreeRepoName(target.RepoPath),
		WorktreeHostPath:    resolveWorktreeHostPath(target.RepoPath),
		SSHDEnabled:         target.EnvConfig.SSHD.Enabled,
		MCPPort:             ports.MCP,
		APIPort:             ports.API,
		SSHPort:             ports.SSH,
		CloudProviderAlias:  target.EnvConfig.CloudProviderAlias,
		ContainerRegistry:   containerRegistry,
		InsecureRegistry:    insecureRegistry,
		RuntimeRegistry:     resolveRuntimeRegistry(target),
		ContainerRegistries: containerRegistries,
		Platform:            ResolveProjectPlatform(target.RepoPath),
		DisableBuildScript:  target.EnvConfig.DisableBuildScript,
		PlatformAccount:     target.EnvConfig.PlatformAccount,
		ImagePullSecrets:    append([]string(nil), target.EnvConfig.ImagePullSecrets...),
		Idle:                target.EnvConfig.Idle,
		Claude:              target.EnvConfig.Claude,
		RuntimePod:          configpkg.NormalizeRuntimePodResources(configpkg.ResolveRuntimePodResourcesForDeploy(target.EnvConfig.RuntimePod, target.Tenant, target.Environment, nil, configpkg.DefaultCgroupRoot)),
		RuntimeDindPod:      configpkg.NormalizeRuntimeDindPodResources(target.EnvConfig.RuntimeDindPod),
		NamespaceQuota:      target.EnvConfig.NamespaceQuota,
		Stopped:             target.EnvConfig.Stopped,
		Version:             version,
		Timeout:             rolloutTimeout,
	}, nil
}

// resolveProjectContainerRegistry resolves the registry the cluster pulls from
// — the DEPLOY-marked entry of the environment's configured registry list.
// Returns "" when the project is uninitialized or marks no list, so callers'
// own fallbacks (deploy provenance, default) still apply.
func resolveProjectContainerRegistry(projectRoot, environment string) string {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return ""
	}
	projectConfig, _, err := configpkg.LoadProjectConfig(projectRoot)
	if err != nil {
		return ""
	}
	list := projectConfig.ContainerRegistriesForEnvironment(environment)
	if list.IsZero() {
		return ""
	}
	registry, _ := list.DeployRegistry()
	return registry
}

// ResolveProjectPlatform loads the per-instance platform config from the
// project's .erun/config.yaml and returns it with defaults resolved. Returns a
// zero PlatformConfig when the project is uninitialized or declares no platform
// block, so deploy threads no platform.* values for non-platform projects.
//
// On the `erun deploy` / `build --deploy` path the block is validated up front
// (loadProjectK8sPlanForRepo), so a malformed block fails the plan before
// reaching here. The open-runtime deploy path does not pass through that plan
// step, so it reaches here unvalidated — harmless today because the runtime
// chart it deploys ignores the threaded platform.* values (only the platform
// singletons read them, and open never deploys one). Do not rely on this
// function having validated the block.
func ResolveProjectPlatform(projectRoot string) configpkg.PlatformConfig {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return configpkg.PlatformConfig{}
	}
	projectConfig, _, err := configpkg.LoadProjectConfig(projectRoot)
	if err != nil {
		return configpkg.PlatformConfig{}
	}
	return projectConfig.Platform.Resolve()
}

func applyCloudContextStopMetadata(store configpkg.CloudReadStore, env configpkg.EnvConfig, deployInput *HelmDeploySpec) {
	if deployInput == nil {
		return
	}
	status, ok, err := configpkg.FindCloudContextForKubernetesContext(store, env.KubernetesContext)
	if err != nil || !ok {
		return
	}
	deployInput.CloudContextName = status.Name
	deployInput.CloudProvider = status.Provider
	deployInput.CloudProviderAlias = status.CloudProviderAlias
	deployInput.CloudRegion = status.Region
	deployInput.CloudInstanceID = status.InstanceID
}

func applyCloudProviderDeployMetadata(store configpkg.CloudReadStore, env configpkg.EnvConfig, deployInput *HelmDeploySpec) {
	if deployInput == nil {
		return
	}
	alias := strings.TrimSpace(env.CloudProviderAlias)
	deployInput.CloudProviderAlias = alias
	if alias != "" {
		deployInput.CloudProvider = resolveCloudProviderForAlias(store, alias, deployInput.CloudProvider)
	}

	status, ok, err := configpkg.FindCloudContextForKubernetesContext(store, env.KubernetesContext)
	if err == nil && ok {
		if alias == "" || strings.TrimSpace(status.CloudProviderAlias) == alias {
			deployInput.CloudContextName = status.Name
			deployInput.CloudProvider = status.Provider
			deployInput.CloudProviderAlias = status.CloudProviderAlias
			deployInput.CloudRegion = status.Region
			return
		}
	}
	// An env with an AWS alias but no cloud context (a local cluster, say) still
	// needs a region: without one every AWS call in its pod has to be told the
	// region by hand.
	if deployInput.CloudRegion == "" && deployInput.CloudProvider == configpkg.CloudProviderAWS {
		deployInput.CloudRegion = cloud.ResolveEnvironmentAWSRegion(store, env)
	}
}

// resolveCloudProviderForAlias resolves the cloud provider for a non-empty
// alias, preferring the store's configured provider and falling back to the
// provider parsed from the alias suffix. When neither resolves, the existing
// value is preserved.
func resolveCloudProviderForAlias(store configpkg.CloudReadStore, alias, current string) string {
	if provider, err := cloud.ResolveCloudProvider(store, alias); err == nil {
		return provider.Provider
	}
	if provider := cloudProviderFromAlias(alias); provider != "" {
		return provider
	}
	return current
}

func cloudProviderFromAlias(alias string) string {
	_, provider, ok := strings.Cut(strings.TrimSpace(alias), "@")
	if !ok {
		return ""
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == configpkg.CloudProviderAWS {
		return provider
	}
	return ""
}

// resolveWorktreeStorage decides the deploy chart's worktree-storage value.
// Every valid type has an explicit case: a deploy plan must never reach a host
// env (deploy refuses it before spec-building — see
// refuseHostEnvironmentOperation), so that case panics rather than silently
// answering a question a host env does not have.
func resolveWorktreeStorage(target configpkg.OpenResult) string {
	if target.EnvConfig.Type.IsValid() {
		switch target.EnvConfig.Type {
		case configpkg.EnvironmentTypeRuntime:
			// A runtime env is sourceless by default; opting into a mutable
			// source worktree gives it a PVC checkout the pod clones at boot.
			if target.EnvConfig.MountsRuntimeSource() {
				return WorktreeStoragePVC
			}
			return WorktreeStorageNone
		case configpkg.EnvironmentTypeRemoteAgent:
			return WorktreeStoragePVC
		case configpkg.EnvironmentTypeLocalAgent:
			return WorktreeStorageHost
		case configpkg.EnvironmentTypeHost:
			panic("resolveWorktreeStorage: host environments have no pod; a deploy plan must not be built for one")
		default:
			panic(fmt.Sprintf("resolveWorktreeStorage: unhandled EnvironmentType %q", target.EnvConfig.Type))
		}
	}
	if target.RemoteRepo() {
		return WorktreeStoragePVC
	}
	return WorktreeStorageHost
}

func resolveWorktreeRepoName(repoPath string) string {
	repoName := strings.TrimSpace(filepath.Base(strings.TrimSpace(repoPath)))
	if repoName == "" || repoName == "." || repoName == string(filepath.Separator) {
		return "worktree"
	}
	return repoName
}

func resolveWorktreeHostPath(repoPath string) string {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return ""
	}

	if runtime.GOOS == "windows" {
		// A remote/PVC env's repo path is a Linux path on the pod's filesystem
		// (e.g. /home/erun/git/<repo>); it must never be run through the host's
		// OS-native filepath, which rewrites it to \home\erun\git\<repo>. A rooted
		// path with no drive letter is never a real local Windows path, so treat
		// it as the remote Linux path it is and normalize to forward slashes.
		if rootedWithoutDrive(repoPath) {
			return path.Clean(filepath.ToSlash(repoPath))
		}
		// A local drive path (C:\...) is a local-agent env's host worktree. The
		// local k3s node runs in WSL2, which exposes each Windows drive under
		// /mnt/<drive>; the raw C:\... path is both invisible to that Linux node
		// and mangled by helm --set backslash escaping, so mount it through the
		// WSL2 view instead.
		return WindowsDrivePathToWSLMount(filepath.Clean(repoPath))
	}

	cleaned := filepath.Clean(repoPath)
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil || strings.TrimSpace(resolved) == "" {
		return cleaned
	}

	return resolved
}

// WindowsDrivePathToWSLMount rewrites a Windows drive path to the mount a WSL2
// node sees it at (C:\Users\me\repo -> /mnt/c/Users/me/repo). Backslashes are
// converted explicitly rather than via filepath.ToSlash so the translation is
// correct and testable on any host, not only where the OS separator is "\".
// A path that does not start with a drive letter passes through slash-normalized,
// so the transform is inert for anything already POSIX-shaped.
func WindowsDrivePathToWSLMount(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if len(p) < 2 || p[1] != ':' {
		return p
	}
	drive := p[0]
	if (drive < 'A' || drive > 'Z') && (drive < 'a' || drive > 'z') {
		return p
	}
	rest := strings.TrimPrefix(p[2:], "/")
	mount := "/mnt/" + strings.ToLower(string(drive))
	if rest == "" {
		return mount
	}
	return mount + "/" + rest
}

// rootedWithoutDrive reports whether a path is absolute (rooted at / or \) but
// carries no Windows drive letter — the shape of a remote Linux path, never a
// real local Windows path.
func rootedWithoutDrive(p string) bool {
	if len(p) >= 2 && p[1] == ':' {
		return false
	}
	return strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`)
}

// Params pairs the full deploy spec with the rollout's output writers. The whole
// spec rides along, so the executed command (built from it in helmDeployCommandSpec)
// stays identical to the traced one — no field can be dropped in transit.
func (d HelmDeploySpec) Params(stdout, stderr io.Writer) HelmDeployParams {
	return HelmDeployParams{HelmDeploySpec: d, Stdout: stdout, Stderr: stderr}
}
