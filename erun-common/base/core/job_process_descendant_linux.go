//go:build linux

package core

import (
	"bufio"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// prSetChildSubreaper is PR_SET_CHILD_SUBREAPER; package syscall does not define it.
const prSetChildSubreaper = 36

// EnableEnvironmentJobSubreaper makes orphaned descendants reparent to this
// process (inherited across fork). Best-effort: refusal only narrows coverage.
func EnableEnvironmentJobSubreaper() {
	_, _, _ = syscall.Syscall(syscall.SYS_PRCTL, prSetChildSubreaper, 1, 0)
}

// EnvironmentJobDescendantSurvivors reports a live process reparented onto
// parentPID after the tracked child was reaped; it catches setsid escapes. It
// uses the same settle window because reparenting can land just after Wait returns.
func EnvironmentJobDescendantSurvivors(parentPID int, baseline map[int]struct{}) bool {
	if parentPID <= 0 {
		return false
	}
	deadline := time.Now().Add(EnvironmentJobProcessGroupSurvivorSettleWindow)
	for {
		alive := environmentJobDescendantHasLiveMember(parentPID, baseline)
		if !alive || !time.Now().Before(deadline) {
			return alive
		}
		time.Sleep(EnvironmentJobProcessGroupSurvivorSettlePoll)
	}
}

// EnvironmentJobDescendantBaseline records pids already parented to this process
// before the work starts, so a shared process's own children are excluded.
func EnvironmentJobDescendantBaseline(parentPID int) map[int]struct{} {
	baseline := map[int]struct{}{}
	procs, ok := EnvironmentJobSessionProcessesFunc()
	if !ok {
		return baseline
	}
	for _, proc := range procs {
		if proc.Parent == parentPID {
			baseline[proc.Pid] = struct{}{}
		}
	}
	return baseline
}

// environmentJobDescendantHasLiveMember prefers /proc and falls back to ps.
func environmentJobDescendantHasLiveMember(parentPID int, baseline map[int]struct{}) bool {
	if procs, ok := EnvironmentJobSessionProcessesFunc(); ok {
		return descendantHasLiveMember(procs, parentPID, baseline)
	}
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,stat=").Output()
	if err != nil {
		return false
	}
	return parsePSDescendantTable(out, parentPID, baseline)
}

// parsePSDescendantTable finds a live, non-zombie child of parentPID in
// `ps -axo pid=,ppid=,stat=` output.
func parsePSDescendantTable(out []byte, parentPID int, baseline map[int]struct{}) bool {
	target := strconv.Itoa(parentPID)
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 || fields[1] != target {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		if _, existed := baseline[pid]; existed {
			continue
		}
		if !psStatIsZombie(fields[2]) {
			return true
		}
	}
	return false
}
