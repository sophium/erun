package runtimeresources

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// DefaultRuntimePodCPU/Memory size the runtime container and must match the chart
// fallback in erun-devops/k8s/erun-devops/templates/service.yaml. Memory is sized
// so a cold in-pod lint/test gate peaks near 78% of the limit; 12Gi leaves no headroom.
const (
	DefaultRuntimePodCPU    = "4"
	DefaultRuntimePodMemory = "16384Mi"
)

// DefaultRuntimeDindCPU/Memory size the erun-dind sidecar so the namespace
// LimitRange does not default it to the whole quota; one gate build peaks at ~15.2Gi.
// Only CPU is enforced on build containers (via the build CPU-cap cgroup); memory
// cannot be nested under the pod cgroup, so the memory limit is a capacity figure.
const (
	// MinimumRuntimeDindCPU is the floor below which a gate build spends most of its time throttled.
	MinimumRuntimeDindCPU                   = "4"
	DefaultRuntimeDindCPUNodeCPUMilli int64 = 24000
	// DefaultRuntimeDindCPUCoTenants assumes gate builds are rare bursts, so only two
	// environments on a node build at once.
	DefaultRuntimeDindCPUCoTenants = 2

	DefaultRuntimeDindMemory        = "20Gi"
	DefaultRuntimeDindRequestCPU    = "0.25"
	DefaultRuntimeDindRequestMemory = "1024Mi"
)

// DefaultRuntimeDindCPU must match the chart's runtime.dind.resources.limits.cpu
// fallback (runtime_dind_default_mirrors_test.go).
var DefaultRuntimeDindCPU = RuntimeDindCPULimit(DefaultRuntimeDindCPUNodeCPUMilli, DefaultRuntimeDindCPUCoTenants)

// RuntimeDindCPULimit divides the node's CPUs by coTenants, floored at
// MinimumRuntimeDindCPU and rounded up to whole cores. Limits are ceilings, so
// oversubscription is intended; an unknown node (<= 0) resolves to the floor.
func RuntimeDindCPULimit(nodeCPUMilli int64, coTenants int) string {
	if nodeCPUMilli <= 0 {
		return MinimumRuntimeDindCPU
	}
	if coTenants < 1 {
		coTenants = 1
	}
	perEnvironment := nodeCPUMilli / int64(coTenants)
	if floor, err := ParseKubernetesCPUToMilli(MinimumRuntimeDindCPU); err == nil && perEnvironment < floor {
		perEnvironment = floor
	}
	return FormatKubernetesCPUFromMilli(ScaleMilliToWholeCores(perEnvironment, 1))
}

// DefaultRuntimeInitContainer* size the runtime chart's init containers so they
// do not fall back to LimitRange defaults.
const (
	DefaultRuntimeInitContainerCPU           = "0.5"
	DefaultRuntimeInitContainerMemory        = "256Mi"
	DefaultRuntimeInitContainerRequestCPU    = "0.1"
	DefaultRuntimeInitContainerRequestMemory = "64Mi"
)

// DefaultRuntime*PVCGi mirror the erun-devops chart's PVC sizes.
const (
	DefaultRuntimeHomePVCGi     = 2
	DefaultRuntimeDockerPVCGi   = 50
	DefaultRuntimeWorktreePVCGi = 20
)

// MinimumRuntimeNamespaceQuota is the smallest quota that admits the stock runtime
// pod (both containers plus every PVC). erun-backend-api's tenant quota defaults
// derive from it.
func MinimumRuntimeNamespaceQuota() (cpuMillicores, memoryMB, storageGB int64) {
	devopsCPU, _ := ParseKubernetesCPUToMilli(DefaultRuntimePodCPU)
	dindCPU, _ := ParseKubernetesCPUToMilli(DefaultRuntimeDindCPU)
	devopsMemory, _ := ParseKubernetesMemoryToMi(DefaultRuntimePodMemory)
	dindMemory, _ := ParseKubernetesMemoryToMi(DefaultRuntimeDindMemory)
	storageGB = int64(DefaultRuntimeHomePVCGi + DefaultRuntimeDockerPVCGi + DefaultRuntimeWorktreePVCGi)
	return devopsCPU + dindCPU, devopsMemory + dindMemory, storageGB
}

func NormalizeRuntimePodResources(resources model.RuntimePodResources) model.RuntimePodResources {
	cpu := strings.TrimSpace(resources.CPU)
	if cpu == "" {
		cpu = DefaultRuntimePodCPU
	}
	memory := strings.TrimSpace(resources.Memory)
	if memory == "" {
		memory = DefaultRuntimePodMemory
	}
	return model.RuntimePodResources{CPU: cpu, Memory: memory}
}

func ValidateRuntimePodResources(resources model.RuntimePodResources) error {
	resources = NormalizeRuntimePodResources(resources)
	if _, err := ParseKubernetesCPUToMilli(resources.CPU); err != nil {
		return fmt.Errorf("runtime pod CPU: %w", err)
	}
	if _, err := ParseKubernetesMemoryToMi(resources.Memory); err != nil {
		return fmt.Errorf("runtime pod memory: %w", err)
	}
	return nil
}

// ResolveRuntimePodResourcesForDeploy fills unset CPU/memory from the live cgroup,
// because the in-pod env config never carries runtimepod and defaulting would roll
// back a resized environment. It reads the cgroup only when running in this very
// environment's chart-rendered pod; configured values always win.
func ResolveRuntimePodResourcesForDeploy(configured model.RuntimePodResources, tenant, environment string, env func(string) string, cgroupRoot string) model.RuntimePodResources {
	resolved := configured
	if strings.TrimSpace(resolved.CPU) != "" && strings.TrimSpace(resolved.Memory) != "" {
		return resolved
	}
	if env == nil {
		env = os.Getenv
	}
	if !IsInRuntimeEnvironment(env) {
		return resolved
	}
	podTenant, podEnvironment, inPod := core.InjectedRuntimePodIdentity(env)
	if !inPod || podTenant != strings.TrimSpace(tenant) || podEnvironment != strings.TrimSpace(environment) {
		return resolved
	}
	usage := ReadLocalRuntimeUsage(cgroupRoot)
	if strings.TrimSpace(resolved.CPU) == "" {
		resolved.CPU = liveRuntimePodCPULimit(usage)
	}
	if strings.TrimSpace(resolved.Memory) == "" {
		resolved.Memory = liveRuntimePodMemoryLimit(usage)
	}
	return resolved
}

func liveRuntimePodCPULimit(usage model.RuntimeUsage) string {
	quota := RuntimeQuotaMilli(usage.CPU.QuotaCores)
	if quota <= 0 {
		return ""
	}
	return FormatKubernetesCPUFromMilli(quota)
}

// liveRuntimePodMemoryLimit returns "" for an unlimited cgroup: it names no size to preserve.
func liveRuntimePodMemoryLimit(usage model.RuntimeUsage) string {
	if usage.Memory.Unlimited || usage.Memory.LimitBytes <= 0 {
		return ""
	}
	return FormatBytesAsMi(usage.Memory.LimitBytes)
}

// NormalizeRuntimeDindPodResources is NormalizeRuntimePodResources for the erun-dind sidecar.
func NormalizeRuntimeDindPodResources(resources model.RuntimePodResources) model.RuntimePodResources {
	cpu := strings.TrimSpace(resources.CPU)
	if cpu == "" {
		cpu = DefaultRuntimeDindCPU
	}
	memory := strings.TrimSpace(resources.Memory)
	if memory == "" {
		memory = DefaultRuntimeDindMemory
	}
	return model.RuntimePodResources{CPU: cpu, Memory: memory}
}

func ValidateRuntimeDindPodResources(resources model.RuntimePodResources) error {
	resources = NormalizeRuntimeDindPodResources(resources)
	if _, err := ParseKubernetesCPUToMilli(resources.CPU); err != nil {
		return fmt.Errorf("dind sidecar CPU: %w", err)
	}
	if _, err := ParseKubernetesMemoryToMi(resources.Memory); err != nil {
		return fmt.Errorf("dind sidecar memory: %w", err)
	}
	return nil
}

func ValidateNamespaceResourceQuota(quota model.NamespaceResourceQuota) error {
	if quota.IsZero() {
		return nil
	}
	if strings.TrimSpace(quota.CPU) == "" {
		return fmt.Errorf("namespace quota CPU is required when a namespace quota is set")
	}
	if strings.TrimSpace(quota.Memory) == "" {
		return fmt.Errorf("namespace quota memory is required when a namespace quota is set")
	}
	if strings.TrimSpace(quota.Storage) == "" {
		return fmt.Errorf("namespace quota storage is required when a namespace quota is set")
	}
	if _, err := ParseKubernetesCPUToMilli(quota.CPU); err != nil {
		return fmt.Errorf("namespace quota CPU: %w", err)
	}
	if _, err := ParseKubernetesMemoryToMi(quota.Memory); err != nil {
		return fmt.Errorf("namespace quota memory: %w", err)
	}
	if _, err := ParseKubernetesMemoryToMi(quota.Storage); err != nil {
		return fmt.Errorf("namespace quota storage: %w", err)
	}
	return nil
}

func ParseKubernetesCPUToMilli(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("value is required")
	}
	if strings.HasSuffix(value, "m") {
		milli, err := strconv.ParseInt(strings.TrimSuffix(value, "m"), 10, 64)
		if err != nil || milli <= 0 {
			return 0, fmt.Errorf("must be a positive CPU quantity")
		}
		return milli, nil
	}
	cores, err := strconv.ParseFloat(value, 64)
	if err != nil || cores <= 0 {
		return 0, fmt.Errorf("must be a positive CPU quantity")
	}
	return int64(math.Ceil(cores * 1000)), nil
}

func ParseKubernetesMemoryToMi(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("value is required")
	}
	units := []struct {
		suffix string
		mi     float64
	}{
		{"Ki", 1.0 / 1024.0},
		{"Mi", 1},
		{"Gi", 1024},
		{"Ti", 1024 * 1024},
		{"K", 1000.0 / 1024.0 / 1024.0},
		{"M", 1000.0 / 1024.0},
		{"G", 1000.0 * 1000.0 / 1024.0},
		{"T", 1000.0 * 1000.0 * 1000.0 / 1024.0},
	}
	for _, unit := range units {
		if strings.HasSuffix(value, unit.suffix) {
			amount, err := strconv.ParseFloat(strings.TrimSuffix(value, unit.suffix), 64)
			if err != nil || amount <= 0 {
				return 0, fmt.Errorf("must be a positive memory quantity")
			}
			return int64(math.Ceil(amount * unit.mi)), nil
		}
	}
	bytes, err := strconv.ParseFloat(value, 64)
	if err != nil || bytes <= 0 {
		return 0, fmt.Errorf("must be a positive memory quantity")
	}
	return int64(math.Ceil(bytes / 1024.0 / 1024.0)), nil
}

func FormatKubernetesCPUFromMilli(milli int64) string {
	if milli <= 0 {
		return ""
	}
	if milli%1000 == 0 {
		return strconv.FormatInt(milli/1000, 10)
	}
	return strconv.FormatFloat(float64(milli)/1000.0, 'f', -1, 64)
}
