package eruncommon

import (
	"os"
	"strings"
)

// buildContainerCPUCapCgroupParent names the --cgroup-parent every docker
// build should nest its RUN-instruction containers under, so they inherit
// this environment's real, kubelet-enforced CPU quota instead of escaping it
// as siblings of the erun-dind sidecar's own limited cgroup
// (erun-devops/AGENTS.md's dind cgroup blind-spot notes: BuildKit's docker
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
//
// The hostname in it is deliberate: the path has to name one pod, or two
// environments' builds on a shared node would nest under the same cap and one
// environment's OOM would stop the other's build. That cost is not confined to
// cgroup placement — it is part of the layer key BuildKit computes for every
// RUN in the build, because the flag reaches BuildKit in each RUN's own
// definition rather than as one build-wide input. Measured against buildkit
// 0.21 on the docker driver, changing only --cgroup-parent on the same
// Dockerfile re-executes that RUN and every step after it in the stage, while
// the steps before it stay CACHED. A roll therefore re-runs the RUN
// instructions of any build that has to build for real, even though the
// docker-state volume still holds their layers. A `--mount=type=cache` target
// is the one thing not partitioned by it: its contents are addressed by
// target, and a marker written under one parent was found under another.
//
// It is still never part of erun's own *identity* — the fingerprint
// (computeBuildFingerprint), and the fp-<fingerprint> tag a promotion resolves
// — and that exclusion is what actually keeps a roll cheap: a pod that
// computes the fingerprint its persistent volume already holds promotes that
// image instead of building, and reaches no RUN key at all. The fingerprint
// exclusion and BuildKit's cache key are separate claims, and the first does
// not imply the second: a value can stay out of the fingerprint and still
// partition BuildKit's cache, which is what this one does. Re-keying the path
// so it survives a roll is a real option, traded against the collision safety
// above, and is not decided here.
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
