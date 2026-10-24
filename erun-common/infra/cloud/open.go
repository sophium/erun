package cloud

import (
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

const (
	DefaultShellLaunchWaitTimeout = "2m0s"
)

func ShellLaunchParamsFromResult(result configpkg.OpenResult) configpkg.ShellLaunchParams {
	return configpkg.ShellLaunchParams{
		Dir:                result.RepoPath,
		Tenant:             result.Tenant,
		Environment:        result.Environment,
		Title:              result.Title,
		Namespace:          configpkg.KubernetesNamespaceName(result.Tenant, result.Environment),
		KubernetesContext:  strings.TrimSpace(result.EnvConfig.KubernetesContext),
		RemoteRepo:         result.RemoteRepo(),
		Type:               result.EnvConfig.ResolvedType(),
		ManagedCloud:       result.EnvConfig.ManagedCloud,
		CloudProviderAlias: strings.TrimSpace(result.EnvConfig.CloudProviderAlias),
		Idle:               result.EnvConfig.Idle,
		AITool:             strings.TrimSpace(result.EnvConfig.AITool),
		Claude:             result.EnvConfig.Claude,
		Gateway:            result.Gateway,
		RuntimeImage:       strings.TrimSpace(result.EnvConfig.RuntimeImage),
		RuntimeVersion:     strings.TrimSpace(result.EnvConfig.RuntimeVersion),
		RuntimePod:         result.EnvConfig.RuntimePod,
	}
}

func KubectlDeploymentWaitArgs(req configpkg.ShellLaunchParams) []string {
	args := configpkg.KubectlTargetArgs(req)
	args = append(args, "wait", "--for=condition=Available", "--timeout", DefaultShellLaunchWaitTimeout, "deployment/"+configpkg.RuntimeReleaseName(req.Tenant))
	return args
}
