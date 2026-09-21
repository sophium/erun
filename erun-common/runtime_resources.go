package eruncommon

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// DefaultRuntimePodCPU/Memory size the runtime container itself, and are the
// chart's own fallback for it
// (erun-devops/k8s/erun-devops/templates/service.yaml — keep the two in sync).
//
// DefaultRuntimePodMemory is sized for the heaviest work that container is
// expected to run, not for a serving app: `make check-gate` runs inside it when
// an agent gates its own branch in-pod, and that is the same ten-target gate
// (lint incl. golangci-lint type-checking the AWS SDK, three frontend
// workspaces, Wails/CGO tests, a headless Chromium per Playwright worker) the
// dind sidecar runs during an image build -- which is why the sidecar's own
// default is 20Gi (see DefaultRuntimeDindMemory below). This container sat at
// 8916Mi, sized before that gate grew, i.e. at under half of what the same work
// is sized for on the build side.
//
// The number comes from running that gate in containers of each size on the
// same 6-CPU environment (the same image, `make -j6 check-gate`), "cold"
// meaning a first run after a fresh checkout -- cold Go build and
// golangci-lint caches, which is what a new environment, a branch switch or a
// pruned build cache looks like:
//
//	limit    caches  peak      ceiling hits  result
//	6144Mi   cold    6.00GiB   318+          survived on reclaim alone
//	6144Mi   warm    2.59GiB   0             passed
//	12288Mi  cold    11.71GiB  0             passed, no headroom left
//	12288Mi  warm    4.18GiB   0             passed
//	16384Mi  cold    12.54GiB  0             passed
//	16384Mi  warm    7.17GiB   0             passed
//
// At 6Gi the cold gate has no room at all: it pins the limit and spends the run
// in reclaim. At 12Gi it fits but consumes nearly all of it -- 97.6% -- which is
// what makes 16384Mi this default rather than 12288Mi: a limit just above the
// cold peak is one the next heavier change lands against. At 16384Mi the
// coldest run peaks at 78% of the limit and the warm gate a container runs day
// to day at 45%, both with the ceiling untouched.
//
// Neither share falls much as the limit grows, which is why this default is
// sized to leave the coldest first run a fifth of its limit unused rather than
// to a round multiple of the old one: three of the ten targets (lint, the
// frontend workspaces, the chart tests) size their own fan-out from the memory
// they are given, so a container with more memory runs more of that work at
// once, and a container with too little reclaims against its page cache for the
// whole run instead.
//
// The gate is not the whole story either: this agent's own 6Gi container holds
// 4.83GiB at *idle*, 4.0GiB of it page cache for the repo, node_modules and the
// Go caches, against a memory.peak pinned at 6.00GiB and memory.events `max` at
// 40049 -- it lives in reclaim, and an OOM kill is only a question of which
// allocation outruns it. That is the state an in-pod agent run and its unpushed
// work die in. This default leaves the gate, the agent session, the MCP server
// and that page cache room to share the cgroup instead.
//
// This is a provisioning default, not an enforcement: an environment that
// recorded its own runtimepod.memory keeps it (applyEnvRuntimePod), and one
// already sized below this needs `erun resize --memory` — which `erun list`
// recommends on its own once the cgroup reports an OOM kill or a near-limit
// peak.
const (
	DefaultRuntimePodCPU    = "4"
	DefaultRuntimePodMemory = "16384Mi"
)

// DefaultRuntimeDindCPU/Memory size the erun-dind sidecar's own resource
// limits (erun-devops/k8s/erun-devops/templates/service.yaml). Until #1061 the
// sidecar declared no resources of its own, so a namespace ResourceQuota's
// LimitRange silently defaulted it to the *entire* configured quota width
// (namespaceResourceQuotaManifest in kubernetes_resource_quota.go) — doubling
// what the two-container pod actually asked a ResourceQuota to admit. It was
// then sized to match the erun-devops container's own defaults (8916Mi) —
// the shape the sidecar was already implicitly assigned while that bug was
// live — but every image build runs in this sidecar, not the runtime
// container, and a live cache-miss `erun release` OOMed against that number:
// a single `make check` inside the erun-devops build (golangci-lint
// type-checking the AWS SDK is the driver) measured a peak of ~15.2Gi, so
// 8916Mi was under half of what one gate run alone needs. Raised to 20Gi —
// comfortably above the measured peak, not merely above the old default —
// now that the sidecar is independently sizeable (`erun resize
// --dind-memory`) so an environment that still needs more is not stuck at a
// fixed number. These are only the fallback: an environment that sizes the
// sidecar independently (`erun init`/`erun resize --dind-cpu/--dind-memory`,
// EnvConfig.RuntimeDindPod) overrides them the same way RuntimePod already
// overrides DefaultRuntimePodCPU/Memory.
//
// Note the two axes are not in the same state. CPU *is* now a real ceiling on
// build work: dind-entrypoint.sh mirrors the sidecar's own kubelet-enforced
// cpu.max into a dedicated /docker/erun-build-cpu-cap-<pod> cgroup, and
// buildContainerCPUCapCgroupParent has every `docker build` nest its
// RUN-instruction containers there via `--cgroup-parent`, so the
// sidecar's configured CPU limit throttles a build rather than only sizing the
// concurrency the Dockerfile asks for. That is what makes
// `erun resize --dind-cpu` an effective lever on build CPU, and the cap
// announces itself on stderr when the cgroup plumbing fails (report_uncapped).
//
// Memory has no equivalent, and this default does not by itself guarantee its
// limit is enforced on every cluster: erun-dind's inner `dockerd` runs with no
// `--cgroup-parent` for memory, and on a cgroupfs-driver cgroup v2 host with an
// unnamespaced (privileged) view of the real cgroup tree, that means BuildKit's
// own build containers land as siblings of the pod's own Kubernetes-limited
// cgroup (/sys/fs/cgroup/docker/buildkit/*, memory.max: max) rather than as its
// descendants — verified live. Nesting them properly was investigated and
// shelved:
// it requires moving the sidecar's own process out of its assigned cgroup so
// that cgroup's cgroup.subtree_control can delegate the memory controller to
// a child, and cgroup v2 then refuses to attach any *new* process directly
// to that cgroup afterward (confirmed live) — which is exactly how
// `kubectl exec`/`erun open`, the postStart hook, and the readiness probe
// all reach this container. (The CPU cap above sidesteps that constraint by
// mirroring the value into a sibling cgroup rather than reparenting the
// sidecar, which is why CPU could be enforced where memory could not.)
// Raising this default is real capacity-planning value for memory (it sizes
// `erun init`/`erun resize`'s own suggestion and the backend tenant-quota
// floor derived from MinimumRuntimeNamespaceQuota) even though it is a bigger
// ceiling for the node to have room for, not a hard cgroup enforcement of it.
//
// DefaultRuntimeDindCPU is not a constant: it is RuntimeDindCPULimit's own
// answer for the reference node the fleet is measured on (below). The default
// used to be a flat "4" and that flatness was the bug: a limit of 4 on a
// 24-CPU node leaves the node three-quarters idle while the build inside it is
// throttled, because a CPU limit is a ceiling, not a claim on the node -- the
// scheduler still shares the node fairly between whatever is actually
// runnable, so a build capped well under the node's size is simply slower
// while a co-tenant's own work goes unblocked either way.
const (
	// MinimumRuntimeDindCPU is the floor RuntimeDindCPULimit refuses to size a
	// build below, and the value the flat default used to be. Below this a
	// gate build spends most of its wall clock throttled: the measured shape
	// this replaced was ~80% CPU 'some' pressure at a load average of 2.18 on
	// 24 CPUs, with IO pressure near zero -- quota throttling, not node
	// saturation. It is also what keeps a small or heavily co-tenanted node
	// from being sized into a cap no build can finish under.
	MinimumRuntimeDindCPU = "4"
	// DefaultRuntimeDindCPUNodeCPUMilli is the node this default is sized
	// against: the 24-CPU node the throttling above was measured on, and the
	// size a stock cluster node is provisioned at.
	DefaultRuntimeDindCPUNodeCPUMilli int64 = 24000
	// DefaultRuntimeDindCPUCoTenants is how many of a node's build-capable
	// environments erun assumes are building at once. Two rather than the
	// node's environment count: a gate run is a burst an environment takes
	// rarely, not a steady state every environment holds, so dividing the node
	// by the number of environments merely co-scheduled would size every one
	// of them for a collision that mostly does not happen. An operator whose
	// node really does gate more than that at once resizes down with `erun
	// resize --dind-cpu`, which is the lever this default is a starting point
	// for rather than a substitute for.
	DefaultRuntimeDindCPUCoTenants = 2

	DefaultRuntimeDindMemory        = "20Gi"
	DefaultRuntimeDindRequestCPU    = "0.25"
	DefaultRuntimeDindRequestMemory = "1024Mi"
)

// DefaultRuntimeDindCPU is the dind sidecar's CPU limit for an environment
// that has never been sized (`NormalizeRuntimeDindPodResources`), and the
// namespace-quota floor's own dind term (`MinimumRuntimeNamespaceQuota`). Two
// files cannot read it and write it down again, so both are read back and
// compared against it on every run
// (runtime_dind_default_mirrors_test.go): the erun-devops chart's
// `runtime.dind.resources.limits.cpu` fallback, and the Dockerfile's own
// DIND_CPU_LIMIT ARG default, which is only what a bare `docker build` with no
// erun environment resolved falls back to.
var DefaultRuntimeDindCPU = RuntimeDindCPULimit(DefaultRuntimeDindCPUNodeCPUMilli, DefaultRuntimeDindCPUCoTenants)

// RuntimeDindCPULimit sizes one environment's erun-dind build cap from the
// node it runs on: the node's CPUs divided by the build-capable environments
// expected to be building on it at once, floored at MinimumRuntimeDindCPU and
// rounded up to whole cores.
//
// The divisor is deliberately not a reservation, and cannot be: a Kubernetes
// CPU limit is a ceiling, so the sum of every environment's limit on a node is
// allowed to exceed the node -- what the kernel then does is share the node
// fairly between the builds that are actually running, which is a better
// outcome than each of them being held under a quota too small to use the node
// even when they have it to themselves. What the divisor does buy is the
// honest kind of contention: four environments sized for one node each will
// contend when all four build, and that shows up as real CPU pressure rather
// than as each build being throttled at a quarter of a node nobody else asked
// for. Sizing a node's environments is therefore a choice about how much
// collision to accept, which is why this takes the co-tenant count rather than
// assuming one.
//
// A node capacity of zero or less is the unknown-node case -- nothing has
// established how large the node is -- and resolves to the floor rather than
// to an invented capacity.
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
	return FormatKubernetesCPUFromMilli(scaleMilliToWholeCores(perEnvironment, 1))
}

// DefaultLimitRangeDefaultRequestCPU/Memory size the namespace LimitRange's
// defaultRequest (namespaceResourceQuotaManifest in
// kubernetes_resource_quota.go) — the value an unsized container in the
// namespace is assigned as its own request. Unlike the LimitRange's `default`
// (a limit, which safely equals the namespace cap: it only bounds an unsized
// container and costs nothing at scheduling time), a defaultRequest equal to
// the cap turns the quota into a minimum node size — one unsized container
// reserves the namespace's entire allowance. #1076 was exactly this: an
// unsized init container inherited a request equal to the full quota width,
// and Kubernetes schedules a pod on max(max(init container requests),
// sum(container requests)), so the pod's effective request became the whole
// quota and it could only land on a node at least that large. Sized to the
// runtime container's own small fixed request rather than derived from the
// cap, so an unsized container reserves little regardless of how large the
// namespace quota is configured.
const (
	DefaultLimitRangeDefaultRequestCPU    = "0.25"
	DefaultLimitRangeDefaultRequestMemory = "1024Mi"
)

// DefaultRuntimeInitContainerCPU/Memory and
// DefaultRuntimeInitContainerRequestCPU/Memory size the runtime chart's init
// containers (prepare-volumes, adopt-worktree, install-binfmt in
// erun-devops/k8s/erun-devops/templates/service.yaml). Before #1076 none of
// them declared resources of their own, so each depended entirely on the
// namespace LimitRange's defaults. Each does a few seconds of chown, a file
// copy, or qemu binary registration — none of it CPU- or memory-heavy — so a
// small fixed budget, independent of the namespace cap, covers all three.
const (
	DefaultRuntimeInitContainerCPU           = "0.5"
	DefaultRuntimeInitContainerMemory        = "256Mi"
	DefaultRuntimeInitContainerRequestCPU    = "0.1"
	DefaultRuntimeInitContainerRequestMemory = "64Mi"
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

// resolveRuntimePodResourcesForDeploy fills in the runtime container's CPU
// and/or memory limit from the pod's own live cgroup limits when the env
// config leaves either unset, instead of letting NormalizeRuntimePodResources
// silently substitute its package default. The in-pod
// projected env config never carries runtimepod at all — see
// doctor_sync_config.go's own comment on what the injected env does and does
// not carry — so a deploy driven from an environment's own in-pod MCP/CLI
// edge always resolves an empty RuntimePod, and defaulting it there does not
// mean "no size was ever chosen", it means "this side was never told". A
// deliberately resized environment would then silently roll back to the
// default on its next in-pod deploy.
//
// A configured field always wins over the cgroup read. The read only stands
// in for a field this process was genuinely never told, and only once it can
// prove it is reading the very pod about to be deployed: IsInRuntimeEnvironment
// confirms ERUN_ENV_TYPE names a real chart-rendered pod (never set off-pod,
// unlike ERUN_TENANT/ERUN_ENVIRONMENT alone — erun-integration's own
// in-pod-shaped deploy scenarios set only the latter pair to simulate pod
// identity without a real pod's cgroup behind it, and reading this host's
// unrelated cgroup there would have made the resolved value depend on
// whatever machine happened to run the test), and the tenant/environment
// match confirms it is this pod, not some other environment's, that the
// cgroup limits belong to. A host-side deploy (no injected identity) or a
// deploy of a different environment leaves the configured value untouched,
// so NormalizeRuntimePodResources' default still applies exactly as before
// for a brand-new environment that has never been deployed, in-pod or
// otherwise, and so has no live limits to read.
func resolveRuntimePodResourcesForDeploy(configured RuntimePodResources, tenant, environment string, env func(string) string, cgroupRoot string) RuntimePodResources {
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
	podTenant, podEnvironment, inPod := injectedRuntimePodIdentity(env)
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

// liveRuntimePodCPULimit renders a cgroup reading's CPU quota as the same
// kind of string NormalizeRuntimePodResources expects, or "" when the quota
// could not be read (cgroup v1, no quota set).
func liveRuntimePodCPULimit(usage RuntimeUsage) string {
	quota := runtimeQuotaMilli(usage.CPU.QuotaCores)
	if quota <= 0 {
		return ""
	}
	return FormatKubernetesCPUFromMilli(quota)
}

// liveRuntimePodMemoryLimit renders a cgroup reading's memory.max as a Mi
// string, or "" when the limit could not be read or the container is
// unlimited — an unlimited cgroup names no size to preserve.
func liveRuntimePodMemoryLimit(usage RuntimeUsage) string {
	if usage.Memory.Unlimited || usage.Memory.LimitBytes <= 0 {
		return ""
	}
	return formatBytesAsMi(usage.Memory.LimitBytes)
}

// NormalizeRuntimeDindPodResources and ValidateRuntimeDindPodResources are
// NormalizeRuntimePodResources/ValidateRuntimePodResources's counterparts for
// the erun-dind sidecar, now that it is operator-sizeable (`erun init`/`erun
// resize --dind-cpu/--dind-memory`) rather than fixed at
// DefaultRuntimeDindCPU/Memory for every environment.
func NormalizeRuntimeDindPodResources(resources RuntimePodResources) RuntimePodResources {
	cpu := strings.TrimSpace(resources.CPU)
	if cpu == "" {
		cpu = DefaultRuntimeDindCPU
	}
	memory := strings.TrimSpace(resources.Memory)
	if memory == "" {
		memory = DefaultRuntimeDindMemory
	}
	return RuntimePodResources{CPU: cpu, Memory: memory}
}

func ValidateRuntimeDindPodResources(resources RuntimePodResources) error {
	resources = NormalizeRuntimeDindPodResources(resources)
	if _, err := ParseKubernetesCPUToMilli(resources.CPU); err != nil {
		return fmt.Errorf("dind sidecar CPU: %w", err)
	}
	if _, err := ParseKubernetesMemoryToMi(resources.Memory); err != nil {
		return fmt.Errorf("dind sidecar memory: %w", err)
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
