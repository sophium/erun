package deploy

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

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
func guardInPodRuntimeDeploy(env func(string) string, resolvedTarget config.OpenResult, specs []DeploySpec) error {
	podTenant, podEnvironment, inPod := core.InjectedRuntimePodIdentity(env)
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
