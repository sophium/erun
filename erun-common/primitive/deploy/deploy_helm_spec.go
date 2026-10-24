package deploy

import (
	"fmt"
	"io"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/helm"
)

func newHelmDeploySpec(target configpkg.OpenResult, deployContext model.KubernetesDeployContext, versionOverride string) (HelmDeploySpec, error) {
	valuesFilePath, err := resolveKubernetesDeployValuesFile(deployContext.ChartPath, target.Environment)
	if err != nil {
		return HelmDeploySpec{}, err
	}
	return newHelmDeploySpecWithValues(target, deployContext, versionOverride, valuesFilePath)
}

func newHelmDeploySpecWithValues(target configpkg.OpenResult, deployContext model.KubernetesDeployContext, versionOverride, valuesFilePath string) (HelmDeploySpec, error) {
	version := strings.TrimSpace(versionOverride)
	ports := configpkg.LocalPortsForResult(target)

	// Validated here so a bad marker list fails at deploy time, not in-pod.
	containerRegistries, err := configpkg.DeployTargetContainerRegistries(target)
	if err != nil {
		return HelmDeploySpec{}, err
	}

	rolloutTimeout, err := target.EnvConfig.Deploy.Resolve()
	if err != nil {
		return HelmDeploySpec{}, err
	}

	// helm won't pass --sets into subchart scope, so command() prefixes them with
	// the umbrella's subchart key.
	subchartKey := ""
	if deployContextOwnsRuntimeChart(deployContext, target.Tenant) && !isOCIChartReference(deployContext.ChartPath) {
		subchartKey, err = helm.HelmChartRuntimeSubchartKey(deployContext.ChartPath)
		if err != nil {
			return HelmDeploySpec{}, err
		}
	}

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

// resolveProjectContainerRegistry returns "" when unset so callers' fallbacks
// apply.
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

// ResolveProjectPlatform returns the project's platform config with defaults.
// The open-runtime path reaches it unvalidated, so do not rely on validation here.
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
	// Without a region every AWS call in the pod would need one by hand.
	if deployInput.CloudRegion == "" && deployInput.CloudProvider == configpkg.CloudProviderAWS {
		deployInput.CloudRegion = cloud.ResolveEnvironmentAWSRegion(store, env)
	}
}

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

// resolveWorktreeStorage panics for a host env: deploy refuses those earlier.
func resolveWorktreeStorage(target configpkg.OpenResult) string {
	if target.EnvConfig.Type.IsValid() {
		switch target.EnvConfig.Type {
		case configpkg.EnvironmentTypeRuntime:
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
		// A rooted path with no drive letter is the pod's Linux path; host filepath
		// would rewrite its slashes.
		if rootedWithoutDrive(repoPath) {
			return path.Clean(filepath.ToSlash(repoPath))
		}
		// The WSL2 k3s node sees Windows drives under /mnt/<drive>.
		return WindowsDrivePathToWSLMount(filepath.Clean(repoPath))
	}

	cleaned := filepath.Clean(repoPath)
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil || strings.TrimSpace(resolved) == "" {
		return cleaned
	}

	return resolved
}

// WindowsDrivePathToWSLMount rewrites C:\a\b to /mnt/c/a/b on any host OS.
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

func rootedWithoutDrive(p string) bool {
	if len(p) >= 2 && p[1] == ':' {
		return false
	}
	return strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`)
}

// Params carries the whole spec so the executed command matches the traced one.
func (d HelmDeploySpec) Params(stdout, stderr io.Writer) HelmDeployParams {
	return HelmDeployParams{HelmDeploySpec: d, Stdout: stdout, Stderr: stderr}
}
