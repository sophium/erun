package runtimeview

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// CheckEnvironmentHealth checks registry config and runtime deployment from the
// host, since `erun doctor` runs in-pod and cannot see an undeployed env.
func (s *Service) CheckEnvironmentHealth(selection uimodel.Selection) (Health, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("check environment health", selection.Tenant, selection.Environment); err != nil {
		return Health{}, err
	}
	config, _, err := s.d.Store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return Health{}, err
	}
	result := Health{Tenant: selection.Tenant, Environment: selection.Environment}
	result.Checks = []HealthCheck{
		s.registryHealthCheck(selection.Tenant, selection.Environment, config),
		s.runtimeDeployedHealthCheck(selection.Tenant, selection.Environment, config),
	}
	result.Healthy = healthChecksAllOK(result.Checks)
	return result, nil
}

func healthChecksAllOK(checks []HealthCheck) bool {
	for _, check := range checks {
		if check.Status != healthCheckStatusOK {
			return false
		}
	}
	return true
}

func (s *Service) registryHealthCheck(tenant, environment string, config configpkg.EnvConfig) HealthCheck {
	list, inherited := s.d.EffectiveContainerRegistries(tenant, environment, config)
	check := HealthCheck{ID: healthCheckIDRegistry, Title: "Container registry"}
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

func registryHealthDetail(list configpkg.ContainerRegistries, inherited bool) string {
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

func (s *Service) runtimeDeployedHealthCheck(tenant, environment string, config configpkg.EnvConfig) HealthCheck {
	check := HealthCheck{ID: healthCheckIDRuntime, Title: "Runtime deployment"}
	kubeContext := strings.TrimSpace(config.KubernetesContext)
	if kubeContext == "" {
		check.Status = healthCheckStatusUnknown
		check.Detail = "No Kubernetes context is set for this environment, so its deployment state can't be checked."
		return check
	}
	ctx := s.d.Context()
	deployed, err := s.d.CheckRuntimeDeployed(ctx, kubeContext, tenant, environment)
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

// CheckRuntimeDeployed treats only a genuine NotFound as not deployed; any other
// kubectl failure is an error so the check reports "could not check".
func CheckRuntimeDeployed(ctx context.Context, kubeContext, tenant, environment string) (bool, error) {
	namespace := configpkg.KubernetesNamespaceName(tenant, environment)
	output, err := kubectlJSON(ctx, kubeContext,
		"get", "pods", "-n", namespace,
		"-o", `jsonpath={range .items[*]}{range .spec.containers[*]}{.name}{"\n"}{end}{end}`)
	if err != nil {
		// A missing kubectl binary also prints "not found".
		if !errors.Is(err, exec.ErrNotFound) && kube.KubernetesResourceNotFound(err.Error()) {
			return false, nil
		}
		return false, err
	}
	for _, name := range strings.Split(string(output), "\n") {
		if strings.TrimSpace(name) == configpkg.DevopsComponentName {
			return true, nil
		}
	}
	return false, nil
}
