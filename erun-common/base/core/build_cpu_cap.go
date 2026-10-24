package core

import (
	"os"
	"strings"
)

// BuildContainerCPUCapCgroupParent names the --cgroup-parent that makes build
// containers inherit the dind sidecar's CPU quota; BuildKit has no CPU limit flag.
// It applies only in an injected runtime pod and is keyed by pod hostname so
// environments on one node never share a cap. The value is part of BuildKit's RUN
// cache key, so a pod roll re-runs RUN steps; it is excluded from erun's build
// fingerprint.
func BuildContainerCPUCapCgroupParent() string {
	if !InInjectedRuntimePod() {
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
