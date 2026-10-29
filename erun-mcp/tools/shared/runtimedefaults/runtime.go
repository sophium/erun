package runtimedefaults

import (
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/build/dockerimage"
	"github.com/sophium/erun/erun-common/primitive/build/script"
	"github.com/sophium/erun/erun-common/primitive/deploy/helmrun"
	"github.com/sophium/erun/erun-common/primitive/open/podwait"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerregistry"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
)

func NormalizeRuntimeConfig(cfg runtimecfg.RuntimeConfig) runtimecfg.RuntimeConfig {
	cfg = normalizeRuntimeConfigBuildDefaults(cfg)
	return normalizeRuntimeConfigRemoteDefaults(cfg)
}

func normalizeRuntimeConfigBuildDefaults(cfg runtimecfg.RuntimeConfig) runtimecfg.RuntimeConfig {
	if cfg.Store == nil {
		cfg.Store = config.ConfigStore{}
	}
	if cfg.BuildScriptRunner == nil {
		cfg.BuildScriptRunner = script.BuildScriptRunner
	}
	if cfg.BuildDockerImage == nil {
		cfg.BuildDockerImage = dockerimage.DockerImageBuilder
	}
	if cfg.PushDockerImage == nil {
		cfg.PushDockerImage = dockerregistry.DockerImagePusher
	}
	if cfg.DeployHelmChart == nil {
		cfg.DeployHelmChart = helmrun.DeployHelmChart
	}
	return cfg
}

func normalizeRuntimeConfigRemoteDefaults(cfg runtimecfg.RuntimeConfig) runtimecfg.RuntimeConfig {
	namespaceEnsurer := cfg.EnsureKubernetesNamespace
	if namespaceEnsurer == nil {
		namespaceEnsurer = kube.EnsureKubernetesNamespace
	}
	cfg.DeployHelmChart = helmrun.WrapHelmChartDeployerWithNamespaceEnsure(namespaceEnsurer, cfg.DeployHelmChart)
	if cfg.WaitForRemoteRuntime == nil {
		cfg.WaitForRemoteRuntime = podwait.WaitForShellDeployment
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
