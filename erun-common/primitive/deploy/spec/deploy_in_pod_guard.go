package spec

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartref"
)

// guardInPodRuntimeDeploy refuses a runtime-chart deploy from the env's own pod:
// the in-pod config is a thin projection, so host-owned fields (ports, sshd,
// resources) would resolve to defaults and could cut the calling channel.
func guardInPodRuntimeDeploy(env func(string) string, resolvedTarget config.OpenResult, specs []helmdeploy.DeploySpec) error {
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

func firstRuntimeChartSpec(specs []helmdeploy.DeploySpec) (helmdeploy.DeploySpec, bool) {
	for _, spec := range specs {
		if chartref.SpecDeploysRuntimeChart(spec) {
			return spec, true
		}
	}
	return helmdeploy.DeploySpec{}, false
}

func inPodGuardVersionHint(version string) string {
	if version = strings.TrimSpace(version); version != "" {
		return version
	}
	return "<version>"
}
