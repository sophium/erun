package main

import (
	"context"
	"fmt"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// CheckEnvironmentHealth runs the out-of-pod diagnostics the Manage dialog's
// "Check environment" action surfaces: whether an effective container registry
// is configured, and whether the runtime is actually deployed to the env's
// Kubernetes context. Both are checked from the host (config resolution +
// kubectl), not in-pod — `erun doctor` execs into the runtime container and so
// cannot report the "not deployed" case this covers.
func (a *App) CheckEnvironmentHealth(selection uiSelection) (uiEnvironmentHealth, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return uiEnvironmentHealth{}, fmt.Errorf("tenant and environment are required")
	}
	config, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return uiEnvironmentHealth{}, err
	}
	result := uiEnvironmentHealth{Tenant: selection.Tenant, Environment: selection.Environment}
	result.Checks = []uiEnvironmentHealthCheck{
		a.registryHealthCheck(selection.Tenant, selection.Environment, config),
		a.runtimeDeployedHealthCheck(selection.Tenant, selection.Environment, config),
	}
	result.Healthy = healthChecksAllOK(result.Checks)
	return result, nil
}

func healthChecksAllOK(checks []uiEnvironmentHealthCheck) bool {
	for _, check := range checks {
		if check.Status != healthCheckStatusOK {
			return false
		}
	}
	return true
}

// registryHealthCheck reports whether the env resolves to any effective
// container registry, using the same resolution the editor and the build/deploy
// resolvers use.
func (a *App) registryHealthCheck(tenant, environment string, config eruncommon.EnvConfig) uiEnvironmentHealthCheck {
	list, inherited := a.effectiveEnvironmentContainerRegistries(tenant, environment, config)
	check := uiEnvironmentHealthCheck{ID: healthCheckIDRegistry, Title: "Container registry"}
	if list.IsZero() {
		check.Status = healthCheckStatusError
		check.Detail = "No container registry is configured, so this environment cannot build or deploy. Set one in Container registries below."
		check.Fix = healthCheckFixSetRegistry
		return check
	}
	check.Status = healthCheckStatusOK
	check.Detail = registryHealthDetail(list, inherited)
	return check
}

// registryHealthDetail names the registry the environment will act on in plain
// language, so a passing check still tells the operator which registry is in
// effect (and whether it is inherited from the project).
func registryHealthDetail(list eruncommon.ContainerRegistries, inherited bool) string {
	target := "a configured registry"
	if cluster, ok := list.ClusterEntry(); ok {
		target = fmt.Sprintf("in-cluster registry %s.%s:%d", cluster.Service, cluster.Namespace, cluster.Port)
	} else if deploy, ok := list.DeployRegistry(); ok {
		target = deploy
	} else if hosts := list.DistinctRegistries(); len(hosts) > 0 {
		target = hosts[0]
	}
	if inherited {
		return fmt.Sprintf("Using %s (inherited from project).", target)
	}
	return fmt.Sprintf("Using %s.", target)
}

// runtimeDeployedHealthCheck reports whether the env's runtime is deployed to
// its Kubernetes context. It is deliberately out-of-pod: `erun doctor` runs
// inside the runtime container and so cannot observe an undeployed env.
func (a *App) runtimeDeployedHealthCheck(tenant, environment string, config eruncommon.EnvConfig) uiEnvironmentHealthCheck {
	check := uiEnvironmentHealthCheck{ID: healthCheckIDRuntime, Title: "Runtime deployment"}
	kubeContext := strings.TrimSpace(config.KubernetesContext)
	if kubeContext == "" {
		check.Status = healthCheckStatusUnknown
		check.Detail = "No Kubernetes context is set for this environment, so its deployment state can't be checked."
		return check
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	deployed, err := a.deps.checkRuntimeDeployed(ctx, kubeContext, tenant, environment)
	if err != nil {
		check.Status = healthCheckStatusUnknown
		check.Detail = fmt.Sprintf("Could not check the runtime deployment on %s: %s", kubeContext, strings.TrimSpace(err.Error()))
		return check
	}
	if !deployed {
		check.Status = healthCheckStatusError
		check.Detail = fmt.Sprintf("The runtime is not deployed to %s. Deploy it to bring this environment up.", kubeContext)
		check.Fix = healthCheckFixDeploy
		return check
	}
	check.Status = healthCheckStatusOK
	check.Detail = fmt.Sprintf("The runtime is deployed to %s.", kubeContext)
	return check
}

// checkRuntimeDeployed reports whether a pod running the erun-devops runtime
// container exists in the env's namespace on the given context. kubectl exits
// non-zero when the namespace or cluster is absent — the not-deployed state —
// which is treated as "not deployed", not an error, mirroring the convention
// loadClusterRegistry uses for a missing Service.
func checkRuntimeDeployed(ctx context.Context, kubeContext, tenant, environment string) (bool, error) {
	namespace := eruncommon.KubernetesNamespaceName(tenant, environment)
	output, err := kubectlJSON(ctx, kubeContext,
		"get", "pods", "-n", namespace,
		"-o", `jsonpath={range .items[*]}{range .spec.containers[*]}{.name}{"\n"}{end}{end}`)
	if err != nil {
		return false, nil
	}
	for _, name := range strings.Split(string(output), "\n") {
		if strings.TrimSpace(name) == eruncommon.DevopsComponentName {
			return true, nil
		}
	}
	return false, nil
}
