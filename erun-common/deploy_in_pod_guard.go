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

// guardInPodRuntimeDeploy refuses to deploy an env's runtime chart from inside
// that env's own runtime pod, whatever the env's type.
//
// What makes such a resolve untrustworthy is not the environment's type but
// where the configuration came from: the in-pod store is only the projection
// the chart injects (see doctor --sync-config), holding a thin subset of the
// env's real shape. A field the host owns and the projection never received
// silently falls back to a default or to an in-pod observation — an
// unprojected port block derives from the 17000 base, an unconfigured runtime
// pod reads this pod's own cgroup limit, and sshd and the chart registry take
// whatever the projection happens to carry — and the resulting rollout
// reshapes the environment and can cut the very channel that asked for it (an
// in-pod deploy that resolves sshdEnabled=false turns off the sshd serving
// workspace-sync). None of those fields depend on where the worktree lives, so
// none of them are made safe by a remote-agent env owning its worktree in the
// pod. Nothing on the read side can repair this either: the projection is
// deliberately not reconciled with the host's runtime pod sizing, so until the
// authoritative env config is threaded into the pod, the host CLI is the only
// place this deploy can be resolved correctly.
//
// Deliberately narrow in what it blocks: only the runtime chart, and only in
// that env's own pod. A component-only deploy carries no environment shape and
// is untouched, so an env's pod keeps deploying its own components.
func guardInPodRuntimeDeploy(env func(string) string, resolvedTarget OpenResult, specs []DeploySpec) error {
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
	return fmt.Errorf("deploy %s/%s: refusing to deploy the runtime chart from inside this environment's own pod — its ports, sshd state, worktree host path, runtime resources, and chart registry are defined by the host config store, not by the in-pod projection this process reads. Run `erun deploy --tenant %s --environment %s --version %s` from the host CLI instead",
		podTenant, podEnvironment, podTenant, podEnvironment, inPodGuardVersionHint(spec.Deploy.Version))
}

func firstRuntimeChartSpec(specs []DeploySpec) (DeploySpec, bool) {
	for _, spec := range specs {
		if specDeploysRuntimeChart(spec) {
			return spec, true
		}
	}
	return DeploySpec{}, false
}

func inPodGuardVersionHint(version string) string {
	if version = strings.TrimSpace(version); version != "" {
		return version
	}
	return "<version>"
}
