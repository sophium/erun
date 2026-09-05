package eruncommon

import (
	"os"
	"regexp"
	"strconv"
)

var (
	dockerfileDindCPULimitPattern    = regexp.MustCompile(`(?m)^\s*ARG\s+DIND_CPU_LIMIT\b`)
	dockerfileDindMemoryLimitPattern = regexp.MustCompile(`(?m)^\s*ARG\s+DIND_MEMORY_LIMIT_MIB\b`)
)

// dockerfileConsumesDindCPULimit / dockerfileConsumesDindMemoryLimit report
// whether a Dockerfile declares the DIND_CPU_LIMIT / DIND_MEMORY_LIMIT_MIB
// ARGs (erun-devops/docker/erun-devops/Dockerfile and any tenant `-devops`
// Dockerfile derived from it). Only such a Dockerfile gets the matching
// --build-arg; every other build's docker command is unchanged.
func dockerfileConsumesDindCPULimit(dockerfilePath string) bool {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return false
	}
	return dockerfileDindCPULimitPattern.Match(data)
}

func dockerfileConsumesDindMemoryLimit(dockerfilePath string) bool {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return false
	}
	return dockerfileDindMemoryLimitPattern.Match(data)
}

// applyDindResourceBuildArgs threads the building environment's actual
// erun-dind sidecar resource limits (EnvConfig.RuntimeDindPod) into build's
// DindCPULimit/DindMemoryLimitMiB fields when its Dockerfile declares the
// matching ARG. Before this, the erun-devops Dockerfile's own in-build gate
// sized its concurrency off either the ARG's hardcoded default or (for
// scripts/parallel-gate.sh's cgroup reads) the host node's raw capacity,
// since a RUN step in the test stage runs in a cgroup that is a sibling of
// the dind sidecar's own limited cgroup, not a descendant of it, and so
// cannot read the sidecar's real limit off the filesystem itself (erun#2081).
func applyDindResourceBuildArgs(store DockerStore, projectRoot, environment string, build *DockerBuildSpec) {
	needsCPU := dockerfileConsumesDindCPULimit(build.DockerfilePath)
	needsMemory := dockerfileConsumesDindMemoryLimit(build.DockerfilePath)
	if !needsCPU && !needsMemory {
		return
	}
	if store == nil {
		store = ConfigStore{}
	}
	cpuLimit, memoryLimitMiB := dindResourceBuildArgValues(resolveDockerBuildDindPodResources(store, projectRoot, environment))
	if needsCPU {
		build.DindCPULimit = cpuLimit
	}
	if needsMemory {
		build.DindMemoryLimitMiB = memoryLimitMiB
	}
}

// resolveDockerBuildDindPodResources resolves the building environment's
// configured erun-dind sidecar resources. When no environment matches this
// project/environment (e.g. a bare Dockerfile build with no saved env, or a
// store error), it falls back to NormalizeRuntimeDindPodResources' own
// conservative constants (DefaultRuntimeDindCPU/Memory, "4"/"20Gi") — the
// same fixed numbers the sidecar's own chart defaults to and this Dockerfile's
// ARG defaults already hardcode — never to the host node's real capacity,
// which is the exact bug this function exists to avoid reintroducing.
func resolveDockerBuildDindPodResources(store DockerStore, projectRoot, environment string) RuntimePodResources {
	envConfig := resolveDockerBuildEnvConfigForProject(store, projectRoot, environment)
	if envConfig == nil {
		return NormalizeRuntimeDindPodResources(RuntimePodResources{})
	}
	return NormalizeRuntimeDindPodResources(envConfig.RuntimeDindPod)
}

// dindResourceBuildArgValues converts RuntimePodResources into the plain
// integers DIND_CPU_LIMIT/DIND_MEMORY_LIMIT_MIB expect: whole cores (the
// Dockerfile divides this in shell integer arithmetic, so a fractional core
// count would break it — floored, with a 1-core minimum) and whole MiB. A
// value that fails to parse (should not happen for an already-normalized
// RuntimePodResources) yields an empty string, which applyDindResourceBuildArgs
// leaves off the docker command entirely rather than passing a bad literal.
func dindResourceBuildArgValues(resources RuntimePodResources) (cpuLimit, memoryLimitMiB string) {
	if milli, err := ParseKubernetesCPUToMilli(resources.CPU); err == nil {
		cores := milli / 1000
		if cores < 1 {
			cores = 1
		}
		cpuLimit = strconv.FormatInt(cores, 10)
	}
	if mi, err := ParseKubernetesMemoryToMi(resources.Memory); err == nil {
		memoryLimitMiB = strconv.FormatInt(mi, 10)
	}
	return cpuLimit, memoryLimitMiB
}
