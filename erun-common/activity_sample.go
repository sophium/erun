package eruncommon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A lease covers work that cooperates. This covers the rest: a Gradle run, a
// test suite, an agent nobody wrapped. The environment monitor samples the
// processes actually resident in the runtime container and records activity for
// the ones doing work, so uninstrumented work stops reading as idle.
//
// "Doing work" is deliberately a CPU-time rate, not mere residency and not
// "advanced by any amount". An agent parked at a prompt is resident for hours
// and would otherwise pin the environment awake forever — the same failure
// the lease's expiry exists to prevent. A process that burned no CPU since the
// previous sample is furniture — but so, in practice, is one that burned a
// sliver of a tick: scheduler noise, timers, and terminal repaints advance
// `utime`/`stime` by a tick or two even while a session sits at an idle
// prompt (measured against a real parked `claude-real`: 21 ticks — 210ms —
// over a 30s sample, ~0.7% of one core, on every single tick). A strictly-
// greater-than-zero test therefore never lapses. The fix compares the CPU
// delta against elapsed wall time and requires it to clear a rate floor
// before counting as work.

const (
	// ActivityKindProcess is the kind the sampler records under, so the idle
	// markers name sampled work separately from a request that arrived.
	ActivityKindProcess = "process"

	// DefaultProcRoot is where the sampler reads process state. Overridable so a
	// test can point it at a fixture tree.
	DefaultProcRoot = "/proc"

	// residentActivityClockTicksPerSecond converts /proc/[pid]/stat's utime and
	// stime — reported in USER_HZ units — into seconds. USER_HZ is a fixed part
	// of the Linux procfs ABI (unlike the kernel's actual timer frequency) and is
	// 100 on every architecture erun runs on.
	residentActivityClockTicksPerSecond = 100

	// residentActivityCPURateFloor is the minimum share of one CPU core a
	// process must have burned over the sample interval to count as work,
	// measured rather than derived: a parked `claude-real` on a live environment
	// advanced ~0.7% of one core on every 30s tick purely from scheduler and
	// terminal-repaint noise, and cleared the old "advanced at all" test on
	// every sample. 5% sits well clear of that measured noise floor while
	// staying far below what any real build step or agent generation burns,
	// which runs close to a full core.
	residentActivityCPURateFloor = 0.05

	residentActivitySampleFileName = "process-sample.json"
)

// ResidentActivitySample is the previous scan's CPU accounting, keyed so a
// recycled pid cannot be mistaken for the process that held it before.
type ResidentActivitySample struct {
	SampledAt time.Time        `json:"sampledAt"`
	CPU       map[string]int64 `json:"cpu,omitempty"`
}

// ResidentActivityResult is one sampler tick's verdict.
type ResidentActivityResult struct {
	Busy bool `json:"busy"`
	// Processes names the work that advanced, deduplicated, for the operator-
	// facing "what is keeping this busy" line.
	Processes []string               `json:"processes,omitempty"`
	Sample    ResidentActivitySample `json:"-"`
}

// residentActivityCommPrefixes are the process names worth treating as work.
// Matched on a comm prefix because the runtime image wraps its agent tools, so
// the process that actually burns the CPU is `claude-real`, not `claude`.
// Deliberately excludes erun's own binaries: a check that can match the observer
// is not a check.
var residentActivityCommPrefixes = []string{
	"buildctl", "buildkit", "cargo", "cc1", "clang", "claude", "cmake", "codex",
	"containerd", "dockerd", "esbuild", "gcc", "gradle", "java", "jest", "make",
	"maven", "mvn", "ninja", "node", "npm", "pnpm", "pytest", "python", "rustc",
	"tsc", "tsserver", "vite", "webpack", "yarn",
}

// ScanResidentActivity compares this tick's CPU accounting against the previous
// one. A matched process is work when it appeared since the previous sample —
// a build that just started has burned nothing yet — or when its CPU delta
// over the elapsed interval clears residentActivityCPURateFloor. The first-
// ever sample reports idle: with nothing to compare against, residency alone
// would be exactly the false positive this design avoids.
func ScanResidentActivity(procRoot string, selfPID int, previous ResidentActivitySample, now time.Time) (ResidentActivityResult, error) {
	root := strings.TrimSpace(procRoot)
	if root == "" {
		root = DefaultProcRoot
	}
	if now.IsZero() {
		now = time.Now()
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return ResidentActivityResult{Sample: ResidentActivitySample{SampledAt: now}}, nil
		}
		return ResidentActivityResult{}, err
	}

	elapsed := now.Sub(previous.SampledAt)
	result := ResidentActivityResult{Sample: ResidentActivitySample{SampledAt: now, CPU: map[string]int64{}}}
	first := len(previous.CPU) == 0
	busy := map[string]struct{}{}
	for _, entry := range entries {
		comm, key, cpu, ok := readResidentActivityProcess(root, entry.Name(), selfPID)
		if !ok {
			continue
		}
		result.Sample.CPU[key] = cpu
		if first {
			continue
		}
		before, seen := previous.CPU[key]
		if !seen || residentActivityCPUBusy(cpu-before, elapsed) {
			busy[comm] = struct{}{}
		}
	}

	result.Processes = sortedKeys(busy)
	result.Busy = len(result.Processes) > 0
	return result, nil
}

// residentActivityCPUBusy reports whether a CPU-tick delta over the elapsed
// sample interval clears the rate floor. A delta of zero or fewer ticks is
// never busy — that case also covers a clock that did not advance, so the
// rate is never computed against a non-positive interval.
func residentActivityCPUBusy(deltaTicks int64, elapsed time.Duration) bool {
	if deltaTicks <= 0 || elapsed <= 0 {
		return false
	}
	rate := float64(deltaTicks) / residentActivityClockTicksPerSecond / elapsed.Seconds()
	return rate >= residentActivityCPURateFloor
}

// readResidentActivityProcess reads one /proc entry, returning its name, the
// pid+start-time key that survives pid reuse, and its accumulated CPU ticks.
// Entries that are not processes, are the observer itself, or are not work
// report ok=false.
func readResidentActivityProcess(root, name string, selfPID int) (string, string, int64, bool) {
	pid, err := strconv.Atoi(name)
	if err != nil || pid <= 0 || pid == selfPID {
		return "", "", 0, false
	}
	comm, cpu, startTime, ok := readProcessStat(filepath.Join(root, name, "stat"))
	if !ok || !residentActivityProcess(comm) {
		return "", "", 0, false
	}
	return comm, fmt.Sprintf("%d-%d", pid, startTime), cpu, true
}

func residentActivityProcess(comm string) bool {
	for _, prefix := range residentActivityCommPrefixes {
		if strings.HasPrefix(comm, prefix) {
			return true
		}
	}
	return false
}

// readProcessStat returns the process name, its total CPU ticks, and the start
// time that disambiguates a recycled pid. The comm field is parenthesised and
// may itself contain spaces and parentheses, so the split has to start after the
// last ')'.
func readProcessStat(path string) (string, int64, int64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, 0, false
	}
	line := string(data)
	open := strings.IndexByte(line, '(')
	closing := strings.LastIndexByte(line, ')')
	if open < 0 || closing <= open {
		return "", 0, 0, false
	}
	comm := line[open+1 : closing]
	fields := strings.Fields(line[closing+1:])
	// Fields are the /proc stat columns from `state` (column 3) onward, so a
	// column number maps to index column-3.
	const (
		utimeIndex     = 11
		stimeIndex     = 12
		startTimeIndex = 19
	)
	if len(fields) <= startTimeIndex {
		return "", 0, 0, false
	}
	utime, err := strconv.ParseInt(fields[utimeIndex], 10, 64)
	if err != nil {
		return "", 0, 0, false
	}
	stime, err := strconv.ParseInt(fields[stimeIndex], 10, 64)
	if err != nil {
		return "", 0, 0, false
	}
	startTime, err := strconv.ParseInt(fields[startTimeIndex], 10, 64)
	if err != nil {
		return "", 0, 0, false
	}
	return comm, utime + stime, startTime, true
}

func sortedKeys(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// LoadResidentActivitySample reads the previous tick's accounting. A missing or
// unreadable sample is the first-sample case, not an error: the sampler must
// never fail the monitor loop over its own bookkeeping.
func LoadResidentActivitySample(tenant, environment string) (ResidentActivitySample, error) {
	path, err := residentActivitySamplePath(tenant, environment)
	if err != nil {
		return ResidentActivitySample{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ResidentActivitySample{}, nil
	}
	var sample ResidentActivitySample
	if err := json.Unmarshal(data, &sample); err != nil {
		return ResidentActivitySample{}, nil
	}
	return sample, nil
}

func SaveResidentActivitySample(tenant, environment string, sample ResidentActivitySample) error {
	path, err := residentActivitySamplePath(tenant, environment)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(sample)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func residentActivitySamplePath(tenant, environment string) (string, error) {
	dir, err := EnvironmentActivityDir(tenant, environment)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, residentActivitySampleFileName), nil
}
