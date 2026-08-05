package eruncommon

import (
	"fmt"
	"strings"
)

// injectedRuntimePodIdentity returns the tenant/environment the runtime chart
// injected into this process. The chart sets both ERUN_TENANT and
// ERUN_ENVIRONMENT on every runtime container and nothing else does, so the pair
// is the authoritative "I am inside an erun runtime pod, serving this env"
// marker — a kubeconfig or an in-cluster context is not (both exist off-pod too).
func injectedRuntimePodIdentity(env func(string) string) (tenant string, environment string, ok bool) {
	if env == nil {
		return "", "", false
	}
	tenant = strings.TrimSpace(env("ERUN_TENANT"))
	environment = strings.TrimSpace(env("ERUN_ENVIRONMENT"))
	if tenant == "" || environment == "" {
		return "", "", false
	}
	return tenant, environment, true
}

// guardInPodLocalAgentRuntimeDeploy refuses to deploy a local-agent env's runtime
// chart from inside that env's own runtime pod.
//
// A local-agent env is defined by host-side state the pod does not have: the
// operator's checkout is hostPath-mounted, and the env's ports, runtime pod
// shape, and chart registry live in the host config store plus the project's
// .erun/config.yaml. The in-pod store is only the projection the chart injects
// (see doctor --sync-config), so an in-pod resolve silently falls back to
// defaults — port-range base 17000, the in-pod mount path as worktreeHostPath,
// default pod resources, the project's deploy registry for the chart — and the
// resulting rollout reshapes the environment and cuts the very channel that
// asked for it. Until the authoritative env config is threaded into the pod, the
// host CLI is the only place this deploy can be resolved correctly.
//
// Deliberately narrow: only the runtime chart, only local-agent, only in that
// env's own pod. A remote-agent env owns its worktree in the pod and keeps
// deploying itself; component-only deploys carry no environment shape.
func guardInPodLocalAgentRuntimeDeploy(env func(string) string, resolvedTarget OpenResult, specs []DeploySpec) error {
	if resolvedTarget.EnvConfig.ResolvedType() != EnvironmentTypeLocalAgent {
		return nil
	}
	podTenant, podEnvironment, inPod := injectedRuntimePodIdentity(env)
	if !inPod {
		return nil
	}
	if podTenant != strings.TrimSpace(resolvedTarget.Tenant) || podEnvironment != strings.TrimSpace(resolvedTarget.Environment) {
		return nil
	}
	spec, ok := firstRuntimeChartSpec(specs)
	if !ok {
		return nil
	}
	return fmt.Errorf("deploy %s/%s: refusing to deploy the runtime chart from inside this environment's own pod — %s/%s is a local-agent environment, whose ports, worktree host path, runtime resources, and chart registry are defined by the host config store, not by the in-pod projection this process reads. Run `erun deploy --tenant %s --environment %s --version %s` from the host CLI instead",
		podTenant, podEnvironment, podTenant, podEnvironment, podTenant, podEnvironment, inPodGuardVersionHint(spec))
}

func firstRuntimeChartSpec(specs []DeploySpec) (DeploySpec, bool) {
	for _, spec := range specs {
		if specDeploysRuntimeChart(spec) {
			return spec, true
		}
	}
	return DeploySpec{}, false
}

func inPodGuardVersionHint(spec DeploySpec) string {
	if version := strings.TrimSpace(spec.Deploy.Version); version != "" {
		return version
	}
	return "<version>"
}
