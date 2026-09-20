//go:build linux

package eruncommon

import (
	"bufio"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// prSetChildSubreaper is PR_SET_CHILD_SUBREAPER from <linux/prctl.h>. There is
// no constant for it in the syscall package, which wraps only the calls the
// standard library itself uses.
const prSetChildSubreaper = 36

// enableEnvironmentJobSubreaper asks the kernel to hand this process any
// descendant of it that gets orphaned, instead of the ordinary reparent-to-init
// (see descendantHasLiveMember for why the supervisor needs that). The
// attribute is inherited across fork, so it covers every process this
// supervisor starts, however many generations down. Best-effort: a kernel that
// refuses the call leaves the descendant check unable to see anything, which is
// the same answer as the platforms that have no such attribute at all --
// narrower coverage, never a false positive.
func enableEnvironmentJobSubreaper() {
	_, _, _ = syscall.Syscall(syscall.SYS_PRCTL, prSetChildSubreaper, 1, 0)
}

// environmentJobDescendantSurvivors reports whether any *live* process has been
// reparented onto parentPID -- the supervisor itself -- by the time the job's
// own tracked child has already been waited on. It is the widest of the three
// survivor checks and the only one that survives a descendant calling setsid
// for itself; see descendantHasLiveMember.
//
// The settle window is the same one the process-group and session scans use,
// and for the same reason: the reparenting this looks for happens as the
// spawning process exits, which can land a few milliseconds after the tracked
// child is reaped, so a single reading taken the instant Wait returns can miss
// a descendant that is about to arrive.
func environmentJobDescendantSurvivors(parentPID int) bool {
	if parentPID <= 0 {
		return false
	}
	deadline := time.Now().Add(environmentJobProcessGroupSurvivorSettleWindow)
	for {
		alive := environmentJobDescendantHasLiveMember(parentPID)
		if !alive || !time.Now().Before(deadline) {
			return alive
		}
		time.Sleep(environmentJobProcessGroupSurvivorSettlePoll)
	}
}

// environmentJobDescendantHasLiveMember asks this platform's own process table
// first -- /proc on Linux -- and only falls back to ps where the platform has
// no table to offer.
func environmentJobDescendantHasLiveMember(parentPID int) bool {
	if procs, ok := environmentJobSessionProcessesFunc(); ok {
		return descendantHasLiveMember(procs, parentPID)
	}
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,stat=").Output()
	if err != nil {
		return false
	}
	return parsePSDescendantTable(out, parentPID)
}

// parsePSDescendantTable finds a live, non-zombie process whose parent is
// parentPID in `ps -axo pid=,ppid=,stat=` output. Compared against ps's own
// ppid column rather than getsid(2) only because there is no table to prefer
// here; ppid is populated on every platform, unlike the session column.
func parsePSDescendantTable(out []byte, parentPID int) bool {
	target := strconv.Itoa(parentPID)
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 || fields[1] != target {
			continue
		}
		if !psStatIsZombie(fields[2]) {
			return true
		}
	}
	return false
}
