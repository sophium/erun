package activity

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

// The sampler records activity for uninstrumented work resident in the runtime container.
// Work is a CPU-time rate above a floor, not residency: a parked agent burns scheduler
// and repaint noise every tick and would otherwise keep the environment awake forever.

const (
	// ActivityKindProcess is the idle-marker kind for sampled work.
	ActivityKindProcess = "process"

	// DefaultProcRoot is overridable so tests can use a fixture tree.
	DefaultProcRoot = "/proc"

	// USER_HZ is fixed at 100 by the Linux procfs ABI on every supported architecture.
	residentActivityClockTicksPerSecond = 100

	// residentActivityCPURateFloor is a share of one core, set well above the ~0.7% a parked
	// agent was measured to burn and well below a real build.
	residentActivityCPURateFloor = 0.05

	residentActivitySampleFileName = "process-sample.json"
)

// ResidentActivitySample is keyed so a recycled pid is not mistaken for its predecessor.
type ResidentActivitySample struct {
	SampledAt time.Time        `json:"sampledAt"`
	CPU       map[string]int64 `json:"cpu,omitempty"`
}

// ResidentActivityResult is one sampler tick's verdict.
type ResidentActivityResult struct {
	Busy      bool                   `json:"busy"`
	Processes []string               `json:"processes,omitempty"`
	Sample    ResidentActivitySample `json:"-"`
}

// Prefix match because the CPU-burning process may carry a suffix (`claude.exe`). erun's own
// binaries are excluded so the observer cannot match itself.
var residentActivityCommPrefixes = []string{
	"buildctl", "buildkit", "cargo", "cc1", "clang", "claude", "cmake", "codex",
	"containerd", "dockerd", "esbuild", "gcc", "gradle", "java", "jest", "make",
	"maven", "mvn", "ninja", "node", "npm", "pnpm", "pytest", "python", "rustc",
	"tsc", "tsserver", "vite", "webpack", "yarn",
}

// ScanResidentActivity counts a process as work when it is new since the previous sample or
// clears the CPU rate floor. The first sample reports idle.
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

// A non-positive delta is never busy, which also avoids a non-positive interval.
func residentActivityCPUBusy(deltaTicks int64, elapsed time.Duration) bool {
	if deltaTicks <= 0 || elapsed <= 0 {
		return false
	}
	rate := float64(deltaTicks) / residentActivityClockTicksPerSecond / elapsed.Seconds()
	return rate >= residentActivityCPURateFloor
}

// ScanInteractiveSSHSession reports whether an sshd-forked process holds a controlling tty.
// sshd keeps the pty master tty-less itself, so this checks its children. Sync and sftp
// channels request no pty, so only an interactive session trips it.
func ScanInteractiveSSHSession(procRoot string) (bool, error) {
	root := strings.TrimSpace(procRoot)
	if root == "" {
		root = DefaultProcRoot
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	processes, sshdPIDs := scanProcessTreeForSSHDChildren(root, entries)
	for _, info := range processes {
		if info.ttyNr != 0 {
			if _, parentIsSSHD := sshdPIDs[info.ppid]; parentIsSSHD {
				return true, nil
			}
		}
	}
	return false, nil
}

type sshProcessInfo struct {
	ppid  int64
	ttyNr int64
}

func scanProcessTreeForSSHDChildren(root string, entries []os.DirEntry) (map[int64]sshProcessInfo, map[int64]struct{}) {
	processes := make(map[int64]sshProcessInfo)
	sshdPIDs := make(map[int64]struct{})
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		comm, ppid, ttyNr, ok := readProcessCommPPIDAndTTY(filepath.Join(root, entry.Name(), "stat"))
		if !ok {
			continue
		}
		processes[int64(pid)] = sshProcessInfo{ppid: ppid, ttyNr: ttyNr}
		if comm == "sshd" {
			sshdPIDs[int64(pid)] = struct{}{}
		}
	}
	return processes, sshdPIDs
}

// tty_nr is 0 when the process has no controlling terminal.
func readProcessCommPPIDAndTTY(path string) (string, int64, int64, bool) {
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
	// fields starts at column 3 (state), so ppid (4) and tty_nr (7) are indexes 1 and 4.
	const ppidIndex = 1
	const ttyNrIndex = 4
	if len(fields) <= ttyNrIndex {
		return "", 0, 0, false
	}
	ppid, err := strconv.ParseInt(fields[ppidIndex], 10, 64)
	if err != nil {
		return "", 0, 0, false
	}
	ttyNr, err := strconv.ParseInt(fields[ttyNrIndex], 10, 64)
	if err != nil {
		return "", 0, 0, false
	}
	return comm, ppid, ttyNr, true
}

// readResidentActivityProcess keys by pid+start time to survive pid reuse.
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

// comm may contain spaces and parentheses, so split after the last ')'.
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
	// Index = stat column - 3.
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

// LoadResidentActivitySample treats a missing or unreadable sample as the first sample, so
// the monitor never fails on it.
func LoadResidentActivitySample(tenant, environment string) (ResidentActivitySample, error) {
	path, err := residentActivitySamplePath(tenant, environment)
	if err != nil {
		return ResidentActivitySample{}, err
	}
	return readResidentActivitySample(path), nil
}

func readResidentActivitySample(path string) ResidentActivitySample {
	data, err := os.ReadFile(path)
	if err != nil {
		return ResidentActivitySample{}
	}
	var sample ResidentActivitySample
	if err := json.Unmarshal(data, &sample); err != nil {
		return ResidentActivitySample{}
	}
	return sample
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
