package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

const (

	// CgroupV2FSType is the sentinel the reading script prints when
	// $cg/cgroup.controllers exists -- the portable systemd-style cgroup v2
	// test (a cgroup2 mount always has this file at its root, delegated
	// subtree or not; a v1 hierarchy never does). The script used to test
	// `stat -fc %T "$cg"` = "cgroup2fs" instead, which depends on the local
	// `stat` binary recognising the cgroup2 magic number by name -- true for
	// GNU coreutils (the erun-devops runtime container) but not for BusyBox
	// (the erun-dind sidecar's own shell), which reports every filesystem
	// type as "UNKNOWN" regardless of what is actually mounted. That silently
	// made every dind-sidecar reading report "cgroup v2 not detected" on a
	// real cluster even though cgroup v2 was genuinely mounted there,
	// confirmed live by exec'ing into erun-dind directly (busybox stat's
	// docstring has no filesystem-type table at all). The file-existence test
	// has no such binary dependency: `[ -f ... ]` means the same thing
	// everywhere.
	CgroupV2FSType = "cgroup2fs"

	// DefaultCgroupRoot is the container's own cgroup v2 directory, read
	// directly by ReadLocalRuntimeUsage. Overridable so a test can point the
	// reader at a fixture tree.
	DefaultCgroupRoot = "/sys/fs/cgroup"
)

// RuntimeUsage is one environment's live resource reading, scoped to the
// runtime container -- not the job that happens to be running in it, since
// two concurrent jobs in the same container would otherwise let one
// misattribute the other's consumption to itself.
type RuntimeUsage struct {
	Tenant      string             `json:"tenant"`
	Environment string             `json:"environment"`
	CPU         RuntimeCPUUsage    `json:"cpu"`
	Memory      RuntimeMemoryUsage `json:"memory"`
	Disk        []RuntimeDiskUsage `json:"disk"`
	// Warnings are named threshold crossings a caller can branch on without
	// parsing prose -- see the RuntimeUsage*WarnPercent constants.
	Warnings []string `json:"warnings,omitempty"`
	// ExcludesBuilds is true when this environment carries the erun-dind
	// sidecar (EnvironmentType.UsesDindSidecar) -- i.e. CPU and Memory above
	// are scoped to the runtime container alone and cannot see a build
	// actually running in the sidecar's own cgroup. Mirrors the desktop
	// hover card's usageExcludesBuilds/excludesBuilds caveat so both
	// transports disclose the same limitation instead of one silently
	// under-reporting relative to the other. Stays true even when Dind below
	// was read successfully -- it describes CPU/Memory above, which never
	// change meaning regardless of what else this reading also carries.
	ExcludesBuilds bool `json:"excludesBuilds,omitempty"`
	// Requests is what the scheduler admits this environment's pod on -- the
	// runtime container's and the erun-dind sidecar's declared
	// resources.requests, and the pod's effective total (runtime_pod_requests.go)
	// -- so a sizing judgement can tell a *reservation* from the cgroup ceiling
	// CPU and Memory above divide against. A limit costs nothing while unused,
	// so `Mem 0% of 27.0GiB` measured against a 1GiB reservation reads as an
	// environment holding 27GiB when it holds none of it.
	//
	// Nil in dry-run (nothing was read, and a zero request would be a
	// reservation of nothing) and never fabricated: a pod spec that could not
	// be read arrives as a reading carrying Unavailable, so "could not read the
	// reservation" stays distinguishable from "reserves nothing".
	Requests *RuntimeUsageRequests `json:"requests,omitempty"`
	// Dind is the erun-dind sidecar's own CPU/memory reading, populated only
	// on an environment that carries the sidecar (ExcludesBuilds true) and
	// only when its cgroup could actually be read -- nil on any other
	// environment, and nil (not a zero value) when the exec into the sidecar
	// failed, so a caller cannot mistake "could not read it" for "read as
	// zero usage". See the file-level comment for why this exists instead of
	// treating the sidecar as permanently unreadable.
	Dind *RuntimeDindUsage `json:"dind,omitempty"`
}

// RuntimeDindUsage is the erun-dind sidecar's own CPU/memory reading, sampled
// by exec'ing the exact same script RunRuntimeUsage runs against the runtime
// container into the sidecar container instead. It deliberately carries no
// Disk field: the sidecar mounts the same workspace volume the runtime
// container's RuntimeUsage.Disk already reports on, so a second disk reading
// here would just repeat that figure under a name that invites reading it as
// a second, independent filesystem.
type RuntimeDindUsage struct {
	CPU    RuntimeCPUUsage    `json:"cpu"`
	Memory RuntimeMemoryUsage `json:"memory"`
}

// HasCounters reports whether the read found anything worth retaining. A host
// without cgroup v2 yields a reading of nothing, and recording that would
// build a history that can only ever answer "no evidence".
func (u RuntimeUsage) HasCounters() bool {
	return u.Memory.LimitBytes > 0 || u.Memory.PeakBytes > 0 || u.CPU.QuotaCores > 0 || u.CPU.Periods > 0
}

// RuntimeCPUUsage reports quota-relative utilisation. Unavailable is set,
// and every other field left zero, when cgroup v2 is absent or cpu.max
// reports no quota to divide by (an unlimited container has no ceiling to be
// "close to").
type RuntimeCPUUsage struct {
	QuotaCores         float64 `json:"quotaCores,omitempty"`
	UtilizationPercent float64 `json:"utilizationPercent,omitempty"`
	IntervalSeconds    float64 `json:"intervalSeconds,omitempty"`
	// UsageUsec, Periods and ThrottledPeriods are cpu.stat's cumulative
	// counters for the current container lifetime (usage_usec, nr_periods,
	// nr_throttled), carried alongside the interval-based UtilizationPercent
	// above so a consumer that retains readings over time -- RuntimeUsageHistory
	// -- can derive its own rates and throttle ratio from the deltas between
	// two readings, without a second reader.
	UsageUsec        int64  `json:"usageUsec,omitempty"`
	Periods          int64  `json:"periods,omitempty"`
	ThrottledPeriods int64  `json:"throttledPeriods,omitempty"`
	Unavailable      string `json:"unavailable,omitempty"`
}

// RuntimeMemoryUsage mirrors what erun-common/ai_launch.go's post-mortem OOM
// message would like to have had beforehand: current usage, the high-water
// mark, and a real OOM-kill counter instead of "likely out of memory".
type RuntimeMemoryUsage struct {
	CurrentBytes int64 `json:"currentBytes,omitempty"`
	PeakBytes    int64 `json:"peakBytes,omitempty"`
	// PeakObserved is true only when memory.peak was actually read. memory.peak
	// is cgroup v2 and reasonably recent -- cgroup v1 exposes
	// memory.max_usage_in_bytes instead, and older v2 kernels lack it entirely
	// -- so a missing or unparseable memory.peak must not collapse into a
	// reported zero indistinguishable from a genuine idle peak, and must not
	// let a percent-of-limit warning compute against a reading that was never
	// taken.
	PeakObserved   bool    `json:"peakObserved,omitempty"`
	LimitBytes     int64   `json:"limitBytes,omitempty"`
	Unlimited      bool    `json:"unlimited,omitempty"`
	PercentOfLimit float64 `json:"percentOfLimit,omitempty"`
	OOMKills       int64   `json:"oomKills,omitempty"`
	// OOMKillsObserved mirrors PeakObserved: memory.events' oom_kill counter
	// can be as unreadable as memory.peak, and a caller must not read a silent
	// zero as "no kills" when it actually means "could not tell".
	OOMKillsObserved bool `json:"oomKillsObserved,omitempty"`
	// CeilingHits is memory.events' "max" counter: how many times this
	// cgroup's usage hit its memory.max ceiling and the kernel reclaimed hard
	// rather than killing outright. Distinct from OOMKills -- a container can
	// spend a long time pinned against its ceiling under reclaim pressure
	// while never once being OOM-killed, and that state is invisible to
	// OOMKills alone.
	CeilingHits int64 `json:"ceilingHits,omitempty"`
	// CeilingHitsObserved mirrors OOMKillsObserved: a missing or unparseable
	// counter must not collapse into a reported zero indistinguishable from
	// a genuine "never hit the ceiling" reading.
	CeilingHitsObserved bool   `json:"ceilingHitsObserved,omitempty"`
	Unavailable         string `json:"unavailable,omitempty"`
}

// RuntimeDiskUsage reports usage for one watched mount (the workspace path,
// at minimum). TotalBytes/UsedBytes/PercentUsed come from a statfs of the
// whole mount, so they describe the node's filesystem, not this environment's
// own footprint -- see the file-level comment's point 4. NodeShared names
// that explicitly; OwnUsedBytes is the one figure scoped to this environment
// alone.
type RuntimeDiskUsage struct {
	Mount      string `json:"mount"`
	NodeShared bool   `json:"nodeShared,omitempty"`
	TotalBytes int64  `json:"totalBytes,omitempty"`
	UsedBytes  int64  `json:"usedBytes,omitempty"`
	// PercentUsed is the node's own fill level, not this environment's share
	// of it -- see NodeShared.
	PercentUsed float64 `json:"percentUsed,omitempty"`
	// OwnUsedBytes is a `du` of the watched mount, scoped to this
	// environment's own filesystem tree rather than the whole node -- the
	// figure an operator can actually reduce by cleaning up this one
	// environment. OwnUsageObserved mirrors PeakObserved/OOMKillsObserved:
	// `du` timing out or being unreadable must render as unavailable, not a
	// fabricated zero.
	OwnUsedBytes     int64  `json:"ownUsedBytes,omitempty"`
	OwnUsageObserved bool   `json:"ownUsageObserved,omitempty"`
	Unavailable      string `json:"unavailable,omitempty"`
}

func RuntimeCgroupV2Available(values map[string]string) bool {
	return values["cgroup_type"] == CgroupV2FSType
}

func RuntimeMemoryUsageFromValues(v map[string]string) RuntimeMemoryUsage {
	m := RuntimeMemoryUsage{}
	if !RuntimeCgroupV2Available(v) {
		m.Unavailable = "cgroup v2 not detected under /sys/fs/cgroup; memory usage needs memory.current/memory.max"
		return m
	}
	current, ok := core.ParseRuntimeInt64(v["memory_current"])
	if !ok {
		m.Unavailable = "memory.current was not readable"
		return m
	}
	m.CurrentBytes = current
	if peak, ok := core.ParseRuntimeInt64(v["memory_peak"]); ok {
		m.PeakBytes = peak
		m.PeakObserved = true
	}
	if killed, ok := core.ParseRuntimeInt64(v["memory_oom_kill"]); ok {
		m.OOMKills = killed
		m.OOMKillsObserved = true
	}
	if hits, ok := core.ParseRuntimeInt64(v["memory_ceiling_hits"]); ok {
		m.CeilingHits = hits
		m.CeilingHitsObserved = true
	}
	maxRaw := v["memory_max"]
	if maxRaw == "max" {
		m.Unlimited = true
		return m
	}
	limit, ok := core.ParseRuntimeInt64(maxRaw)
	if !ok {
		m.Unavailable = "memory.max was not readable"
		return m
	}
	m.LimitBytes = limit
	if limit > 0 {
		m.PercentOfLimit = 100 * float64(current) / float64(limit)
	}
	return m
}

// runtimeCPUCounters is cpu.max/cpu.stat's cumulative counters, shared by the
// interval-based reading below and ReadLocalRuntimeUsage's point-in-time
// local reading -- both need the same quota and the same raw cpu.stat fields,
// and reading them once keeps a remote read and a local read of the same
// container agreeing on what the numbers mean.
type runtimeCPUCounters struct {
	QuotaCores       float64
	UsageUsec        int64
	Periods          int64
	ThrottledPeriods int64
}

func RuntimeCPUCountersFromValues(v map[string]string) (runtimeCPUCounters, bool) {
	var counters runtimeCPUCounters
	if usage, ok := core.ParseRuntimeInt64(v["cpu_usage_after"]); ok {
		counters.UsageUsec = usage
	}
	if periods, ok := core.ParseRuntimeInt64(v["cpu_periods"]); ok {
		counters.Periods = periods
	}
	if throttled, ok := core.ParseRuntimeInt64(v["cpu_throttled_periods"]); ok {
		counters.ThrottledPeriods = throttled
	}
	quotaUsec, periodUsec, ok := core.ParseRuntimeCPUMax(v["cpu_max"])
	if !ok {
		return counters, false
	}
	counters.QuotaCores = float64(quotaUsec) / float64(periodUsec)
	return counters, true
}

// ReadLocalRuntimeUsage reads the container's own cgroup v2 files directly,
// for the in-pod monitor tick that already runs inside the container being
// measured -- see the file-level comment for why that tick does not kubectl-exec
// into itself. It shares runtimeMemoryUsageFromValues and the cumulative CPU
// counters with RunRuntimeUsage's script-based parser, so this is a second
// acquisition path for the same reader, not a second reader: a local read and
// a kubectl-exec'd read of the same container agree on what the numbers mean.
// It never fails -- an absent or unparseable file reports its field as
// unavailable, because cgroup v1 and a missing mount are ordinary states and a
// caller on the monitor's tick must not be broken by either.
func ReadLocalRuntimeUsage(cgroupRoot string) RuntimeUsage {
	root := strings.TrimSpace(cgroupRoot)
	if root == "" {
		root = DefaultCgroupRoot
	}
	values := readLocalCgroupValues(root)
	return RuntimeUsage{
		CPU:    localRuntimeCPUUsage(values),
		Memory: RuntimeMemoryUsageFromValues(values),
	}
}

// readLocalCgroupValues builds a values map matching the shape the
// kubectl-exec'd script prints, so both acquisition paths run through the
// exact same parser. A file that cannot be read yields an empty value, which
// the parser already treats as unavailable.
func readLocalCgroupValues(root string) map[string]string {
	return map[string]string{
		"cgroup_type":           CgroupV2FSType,
		"memory_current":        readLocalCgroupFile(filepath.Join(root, "memory.current")),
		"memory_max":            readLocalCgroupFile(filepath.Join(root, "memory.max")),
		"memory_peak":           readLocalCgroupFile(filepath.Join(root, "memory.peak")),
		"memory_oom_kill":       localCgroupStatValue(filepath.Join(root, "memory.events"), "oom_kill"),
		"memory_ceiling_hits":   localCgroupStatValue(filepath.Join(root, "memory.events"), "max"),
		"cpu_max":               readLocalCgroupFile(filepath.Join(root, "cpu.max")),
		"cpu_usage_after":       localCgroupStatValue(filepath.Join(root, "cpu.stat"), "usage_usec"),
		"cpu_periods":           localCgroupStatValue(filepath.Join(root, "cpu.stat"), "nr_periods"),
		"cpu_throttled_periods": localCgroupStatValue(filepath.Join(root, "cpu.stat"), "nr_throttled"),
	}
}

func readLocalCgroupFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// localCgroupStatValue reads one "<key> <value>" line out of a cgroup keyed
// file (cpu.stat, memory.events), mirroring the awk lookups the exec'd script
// runs against the same files.
func localCgroupStatValue(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == key {
			return fields[1]
		}
	}
	return ""
}

// localRuntimeCPUUsage is runtimeCPUUsageFromValues without the interval-rate
// half: a local read is a single point in time, so there is no before/after
// pair to diff into a utilisation rate. It still reports the same cumulative
// counters (quota, usage_usec, periods, throttled periods) that
// RuntimeUsageHistory needs to derive its own rates from consecutive ticks.
func localRuntimeCPUUsage(v map[string]string) RuntimeCPUUsage {
	c := RuntimeCPUUsage{}
	if !RuntimeCgroupV2Available(v) {
		c.Unavailable = "cgroup v2 not detected under /sys/fs/cgroup; CPU usage needs cpu.max/cpu.stat"
		return c
	}
	counters, ok := RuntimeCPUCountersFromValues(v)
	c.UsageUsec = counters.UsageUsec
	c.Periods = counters.Periods
	c.ThrottledPeriods = counters.ThrottledPeriods
	if !ok {
		c.Unavailable = "cpu.max reports no quota (unlimited or not readable); utilisation needs a quota to measure against"
		return c
	}
	c.QuotaCores = counters.QuotaCores
	return c
}
