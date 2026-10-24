package kube

import (
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// ConcretizeRegistriesForContext expands a cluster registry entry into push/pull hosts;
// lists without one are returned unchanged. Forwards close via ctx.RegistryForwards.
func ConcretizeRegistriesForContext(ctx core.Context, kubeContext string, list config.ContainerRegistries) (config.ContainerRegistries, error) {
	if !list.HasClusterEntry() {
		return list, nil
	}
	forwards := ctx.RegistryForwards
	if forwards == nil {
		forwards = &ClusterRegistryForwards{}
	}
	resolve := NewClusterRegistryResolver(kubeContext, ClusterRegistryDepsFor(ctx, forwards))
	return list.Concrete(resolve)
}

// ConcretizeDeployTargetRegistries stores the concrete list on EnvConfig and the pull
// host on ClusterPullRegistry; targets without a cluster entry are unchanged.
func ConcretizeDeployTargetRegistries(ctx core.Context, target config.OpenResult) (config.OpenResult, error) {
	list, err := config.DeployTargetContainerRegistries(target)
	if err != nil {
		return target, err
	}
	if !list.HasClusterEntry() {
		return target, nil
	}
	concrete, err := ConcretizeRegistriesForContext(ctx, target.EnvConfig.KubernetesContext, list)
	if err != nil {
		return target, err
	}
	target.EnvConfig.ContainerRegistries = concrete
	if pull, ok := concrete.DeployRegistry(); ok {
		target.ClusterPullRegistry = pull
	}
	if entry, ok := list.ClusterEntry(); ok {
		target.ClusterRegistryInsecure = entry.Insecure
	}
	return target, nil
}
