package erunmcp

import (
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-common/primitive/shared"
	sharedpkg "github.com/sophium/erun/erun-mcp/tools/shared"
)

func normalizeRuntimeConfig(cfg sharedpkg.RuntimeConfig) sharedpkg.RuntimeConfig {
	cfg = normalizeRuntimeConfigBuildDefaults(cfg)
	return normalizeRuntimeConfigRemoteDefaults(cfg)
}

func normalizeRuntimeConfigBuildDefaults(cfg sharedpkg.RuntimeConfig) sharedpkg.RuntimeConfig {
	if cfg.Store == nil {
		cfg.Store = config.ConfigStore{}
	}
	if cfg.BuildScriptRunner == nil {
		cfg.BuildScriptRunner = build.BuildScriptRunner
	}
	if cfg.BuildDockerImage == nil {
		cfg.BuildDockerImage = build.DockerImageBuilder
	}
	if cfg.PushDockerImage == nil {
		cfg.PushDockerImage = shared.DockerImagePusher
	}
	if cfg.DeployHelmChart == nil {
		cfg.DeployHelmChart = deploy.DeployHelmChart
	}
	return cfg
}

func normalizeRuntimeConfigRemoteDefaults(cfg sharedpkg.RuntimeConfig) sharedpkg.RuntimeConfig {
	namespaceEnsurer := cfg.EnsureKubernetesNamespace
	if namespaceEnsurer == nil {
		namespaceEnsurer = kube.EnsureKubernetesNamespace
	}
	cfg.DeployHelmChart = deploy.WrapHelmChartDeployerWithNamespaceEnsure(namespaceEnsurer, cfg.DeployHelmChart)
	if cfg.WaitForRemoteRuntime == nil {
		cfg.WaitForRemoteRuntime = open.WaitForShellDeployment
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
