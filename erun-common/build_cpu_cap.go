package eruncommon

import (
	"os"
	"strings"
)

// buildContainerCPUCapCgroupParent names the --cgroup-parent every docker
// build should nest its RUN-instruction containers under, so they inherit
// this environment's real, kubelet-enforced CPU quota instead of escaping it
// as siblings of the erun-dind sidecar's own limited cgroup (erun#2255,
// erun-devops/AGENTS.md's dind cgroup blind-spot notes: BuildKit's docker
// driver has no CPU-limiting flag of its own, and `docker build --cpuset-cpus`
// is silently ignored — verified against docker 28.1.1/buildx 0.18). It is a
// pure, dry-run-safe computation: the cgroup itself is created and sized from
// the sidecar's own live cpu.max by dind-entrypoint.sh at container startup,
// not by this function or by any docker call this build makes.
//
// Applies only inside an injected runtime pod (a real dind sidecar exists to
// escape) — a bare host build has no sidecar limit to inherit, and capping it
// would throttle a developer's own machine for no reason the Dockerfile can
// see. The path is keyed by the pod's own hostname (shared by every container
// in the pod, including the dind sidecar that mirrors its own cpu.max to the
// same path) so two environments' dind sidecars sharing one node's cgroup
// tree — a real, host-namespace-wide path, not one scoped to this pod — never
// collide on the same cap.
func buildContainerCPUCapCgroupParent() string {
	if !inInjectedRuntimePod() {
		return ""
	}
	hostname, err := os.Hostname()
	if err != nil {
		return ""
	}
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return ""
	}
	return "/docker/erun-build-cpu-cap-" + hostname
}
