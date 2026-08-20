package eruncommon

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	DefaultRuntimePodCPU    = "4"
	DefaultRuntimePodMemory = "8916Mi"
)

// DefaultRuntimeDindCPU/Memory size the erun-dind sidecar's own resource
// limits (erun-devops/k8s/erun-devops/templates/service.yaml). Until #1061 the
// sidecar declared no resources of its own, so a namespace ResourceQuota's
// LimitRange silently defaulted it to the *entire* configured quota width
// (namespaceResourceQuotaManifest in kubernetes_resource_quota.go) — doubling
// what the two-container pod actually asked a ResourceQuota to admit. Sized to
// match the erun-devops container's own defaults: that is the shape the
// sidecar was already implicitly assigned while the bug was live, and it is
// dind that runs the actual docker builds, so it is not the smaller of the
// two containers. Not operator-configurable today, unlike DefaultRuntimePodCPU/
// Memory: no caller sizes the sidecar independently of the main container.
const (
	DefaultRuntimeDindCPU           = "4"
	DefaultRuntimeDindMemory        = "8916Mi"
	DefaultRuntimeDindRequestCPU    = "0.25"
	DefaultRuntimeDindRequestMemory = "1024Mi"
)

// DefaultRuntimeHomePVCGi/DockerPVCGi/WorktreePVCGi mirror the erun-devops
// chart's own PVC sizes (service.yaml's release-home, release-docker, and —
// rendered only when worktreeStorage=pvc — release-worktree claims). Kept as
// named constants, rather than re-derived magic numbers, so
// MinimumRuntimeNamespaceQuota moves if the chart's PVC sizes ever do.
const (
	DefaultRuntimeHomePVCGi     = 2
	DefaultRuntimeDockerPVCGi   = 50
	DefaultRuntimeWorktreePVCGi = 20
)

// MinimumRuntimeNamespaceQuota is the smallest per-environment namespace
// ResourceQuota that can admit the stock erun-devops chart's runtime pod: the
// erun-devops and erun-dind containers' limits summed (a ResourceQuota counts
// every container in the pod, not just the first), plus every PVC the chart
// can render — the worktree claim included, so the floor is never short for a
// remote-agent (worktreeStorage=pvc) environment. #1061 was this floor being
// sized for one container instead of two: erun-backend-api's default tenant
// quota (repository.DefaultMax*) and its pre-provision admission check
// (routes.validateNamespaceQuotaFloor) both derive from this function so they
// cannot drift back out of sync with each other or with the chart.
func MinimumRuntimeNamespaceQuota() (cpuMillicores, memoryMB, storageGB int64) {
	devopsCPU, _ := ParseKubernetesCPUToMilli(DefaultRuntimePodCPU)
	dindCPU, _ := ParseKubernetesCPUToMilli(DefaultRuntimeDindCPU)
	devopsMemory, _ := ParseKubernetesMemoryToMi(DefaultRuntimePodMemory)
	dindMemory, _ := ParseKubernetesMemoryToMi(DefaultRuntimeDindMemory)
	storageGB = int64(DefaultRuntimeHomePVCGi + DefaultRuntimeDockerPVCGi + DefaultRuntimeWorktreePVCGi)
	return devopsCPU + dindCPU, devopsMemory + dindMemory, storageGB
}

type RuntimePodResources struct {
	CPU    string `yaml:"cpu,omitempty" json:"cpu,omitempty"`
	Memory string `yaml:"memory,omitempty" json:"memory,omitempty"`
}

func NormalizeRuntimePodResources(resources RuntimePodResources) RuntimePodResources {
	cpu := strings.TrimSpace(resources.CPU)
	if cpu == "" {
		cpu = DefaultRuntimePodCPU
	}
	memory := strings.TrimSpace(resources.Memory)
	if memory == "" {
		memory = DefaultRuntimePodMemory
	}
	return RuntimePodResources{CPU: cpu, Memory: memory}
}

func ValidateRuntimePodResources(resources RuntimePodResources) error {
	resources = NormalizeRuntimePodResources(resources)
	if _, err := ParseKubernetesCPUToMilli(resources.CPU); err != nil {
		return fmt.Errorf("runtime pod CPU: %w", err)
	}
	if _, err := ParseKubernetesMemoryToMi(resources.Memory); err != nil {
		return fmt.Errorf("runtime pod memory: %w", err)
	}
	return nil
}

// NamespaceResourceQuota is a hard per-environment-namespace ceiling on
// CPU/memory/storage, enforced by Kubernetes via a ResourceQuota + LimitRange
// applied to the namespace at deploy time (kubernetes_resource_quota.go). It is
// distinct from RuntimePodResources: RuntimePodResources sizes the runtime
// pod's own container; NamespaceResourceQuota caps everything that namespace
// can ever hold, including future non-runtime workloads. All three fields must
// be set together — a namespace ResourceQuota is meaningless with only some
// resources capped, since Kubernetes then admits unbounded amounts of the
// uncapped ones.
type NamespaceResourceQuota struct {
	CPU     string `yaml:"cpu,omitempty" json:"cpu,omitempty"`
	Memory  string `yaml:"memory,omitempty" json:"memory,omitempty"`
	Storage string `yaml:"storage,omitempty" json:"storage,omitempty"`
}

// IsZero reports whether no cap was configured, so deploy applies no
// ResourceQuota/LimitRange at all rather than an incomplete one.
func (q NamespaceResourceQuota) IsZero() bool {
	return q == NamespaceResourceQuota{}
}

func ValidateNamespaceResourceQuota(quota NamespaceResourceQuota) error {
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
