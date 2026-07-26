package eruncommon

// concretizeRegistriesForContext expands any cluster entry in list into concrete
// push/pull hosts using ctx's forward lifecycle and the given kube-context. A
// list with no cluster entry is returned unchanged (kube-context and forwards
// unused), so non-cluster envs are byte-for-byte unaffected. The port-forward a
// host build needs is opened against ctx.RegistryForwards so the command entry
// closes it once; a nil handle falls back to a local throwaway (dry-run, tests).
func concretizeRegistriesForContext(ctx Context, kubeContext string, list ContainerRegistries) (ContainerRegistries, error) {
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

// concretizeDeployTargetRegistries expands a cluster entry on the deploy target
// into concrete hosts and stashes the result on the target: the concrete list on
// EnvConfig (so both the pull-address resolution and the from→to copy read
// concrete hosts) and the pull host on ClusterPullRegistry (so the chart renders
// it). A target with no cluster entry is returned unchanged, keeping plain envs
// byte-for-byte identical.
func concretizeDeployTargetRegistries(ctx Context, target OpenResult) (OpenResult, error) {
	list, err := deployTargetContainerRegistries(target)
	if err != nil {
		return target, err
	}
	if !list.HasClusterEntry() {
		return target, nil
	}
	concrete, err := concretizeRegistriesForContext(ctx, target.EnvConfig.KubernetesContext, list)
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
