package dockerbuild

import (
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
)

var (
	dockerfileDindCPULimitPattern    = regexp.MustCompile(`(?m)^\s*ARG\s+DIND_CPU_LIMIT\b`)
	dockerfileDindMemoryLimitPattern = regexp.MustCompile(`(?m)^\s*ARG\s+DIND_MEMORY_LIMIT_MIB\b`)
)

// DindCPULimitEnvVar / DindMemoryLimitMiBEnvVar carry the dind sidecar's
// limits via the downward API, since the in-pod config store has no
// environment. They size test concurrency only; the CPU cap itself is enforced
// through buildContainerCPUCapCgroupParent.
const (
	DindCPULimitEnvVar       = "ERUN_DIND_CPU_LIMIT"
	DindMemoryLimitMiBEnvVar = "ERUN_DIND_MEMORY_LIMIT_MIB"
)

// Only a Dockerfile declaring the ARG gets the matching --build-arg.
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

// ApplyDindResourceBuildArgs passes the sidecar's limits because a RUN step's
// cgroup is a sibling of the sidecar's and cannot read them itself.
func ApplyDindResourceBuildArgs(store seam.DockerStore, projectRoot, environment string, build *seam.DockerBuildSpec) {
	needsCPU := dockerfileConsumesDindCPULimit(build.DockerfilePath)
	needsMemory := dockerfileConsumesDindMemoryLimit(build.DockerfilePath)
	if !needsCPU && !needsMemory {
		return
	}
	if store == nil {
		store = config.ConfigStore{}
	}
	cpuLimit, memoryLimitMiB := dindResourceBuildArgValues(ResolveDockerBuildDindPodResources(store, projectRoot, environment))
	if needsCPU {
		build.DindCPULimit = cpuLimit
	}
	if needsMemory {
		build.DindMemoryLimitMiB = memoryLimitMiB
	}
}

// ResolveDockerBuildDindPodResources prefers the downward-API vars per field,
// then the config store, then the normalized defaults — never the host node's
// capacity.
func ResolveDockerBuildDindPodResources(store seam.DockerStore, projectRoot, environment string) model.RuntimePodResources {
	fallback := model.RuntimePodResources{}
	if envConfig := ResolveDockerBuildEnvConfigForProject(store, projectRoot, environment); envConfig != nil {
		fallback = envConfig.RuntimeDindPod
	}
	return config.NormalizeRuntimeDindPodResources(dindPodResourcesPreferEnv(fallback))
}

// dindPodResourcesPreferEnv ignores malformed vars; the memory var is plain MiB.
func dindPodResourcesPreferEnv(fallback model.RuntimePodResources) model.RuntimePodResources {
	resources := fallback
	if cpu := strings.TrimSpace(os.Getenv(DindCPULimitEnvVar)); cpu != "" {
		if _, err := config.ParseKubernetesCPUToMilli(cpu); err == nil {
			resources.CPU = cpu
		}
	}
	if memoryMiB := strings.TrimSpace(os.Getenv(DindMemoryLimitMiBEnvVar)); memoryMiB != "" {
		memory := memoryMiB + "Mi"
		if _, err := config.ParseKubernetesMemoryToMi(memory); err == nil {
			resources.Memory = memory
		}
	}
	return resources
}

// dindResourceBuildArgValues floors CPU to whole cores (min 1) because the
// Dockerfile uses shell integer arithmetic.
func dindResourceBuildArgValues(resources model.RuntimePodResources) (cpuLimit, memoryLimitMiB string) {
	if milli, err := config.ParseKubernetesCPUToMilli(resources.CPU); err == nil {
		cores := milli / 1000
		if cores < 1 {
			cores = 1
		}
		cpuLimit = strconv.FormatInt(cores, 10)
	}
	if mi, err := config.ParseKubernetesMemoryToMi(resources.Memory); err == nil {
		memoryLimitMiB = strconv.FormatInt(mi, 10)
	}
	return cpuLimit, memoryLimitMiB
}
