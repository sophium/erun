package erunmcp

import (
	"github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

func normalizeRuntimeConfig(cfg shared.RuntimeConfig) shared.RuntimeConfig {
	cfg = normalizeRuntimeConfigBuildDefaults(cfg)
	return normalizeRuntimeConfigRemoteDefaults(cfg)
}

func normalizeRuntimeConfigBuildDefaults(cfg shared.RuntimeConfig) shared.RuntimeConfig {
	if cfg.Store == nil {
		cfg.Store = config.ConfigStore{}
	}
	if cfg.BuildScriptRunner == nil {
		cfg.BuildScriptRunner = eruncommon.BuildScriptRunner
	}
	if cfg.BuildDockerImage == nil {
		cfg.BuildDockerImage = eruncommon.DockerImageBuilder
	}
	if cfg.PushDockerImage == nil {
		cfg.PushDockerImage = eruncommon.DockerImagePusher
	}
	if cfg.DeployHelmChart == nil {
		cfg.DeployHelmChart = eruncommon.DeployHelmChart
	}
	return cfg
}

func normalizeRuntimeConfigRemoteDefaults(cfg shared.RuntimeConfig) shared.RuntimeConfig {
	namespaceEnsurer := cfg.EnsureKubernetesNamespace
	if namespaceEnsurer == nil {
		namespaceEnsurer = kube.EnsureKubernetesNamespace
	}
	cfg.DeployHelmChart = eruncommon.WrapHelmChartDeployerWithNamespaceEnsure(namespaceEnsurer, cfg.DeployHelmChart)
	if cfg.WaitForRemoteRuntime == nil {
		cfg.WaitForRemoteRuntime = eruncommon.WaitForShellDeployment
	}
	if cfg.RunRemoteCommand == nil {
		cfg.RunRemoteCommand = kube.RunRemoteCommand
	}
	if cfg.DeleteKubernetesNamespace == nil {
		cfg.DeleteKubernetesNamespace = kube.DeleteKubernetesNamespace
	}
	if cfg.ResolveRuntimeRegistryVersions == nil {
		cfg.ResolveRuntimeRegistryVersions = registry.ResolveDefaultRuntimeRegistryVersions
	}
	return cfg
}
