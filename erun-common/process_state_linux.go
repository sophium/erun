//go:build linux

package eruncommon

import (
	"os"
	"strconv"
)

// platformProcessZombie answers whether pid names a process that has exited and
// is only waiting to be reaped, from /proc/<pid>/stat.
//
// The second return is false when this host cannot answer for that pid at all —
// the entry is gone, so the process no longer exists, or it is not readable
// under a hidepid mount. Neither is a claim that the process is running, and
// the caller falls back rather than trusting a default answer.
func platformProcessZombie(pid int) (bool, bool) {
	if pid <= 0 {
		return false, false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false, false
	}
	proc, ok := parseProcStat(stat)
	// The entry no longer describes the pid that was asked about, so the pid
	// may have been reused since the read started; it is not this caller's
	// process, and answering for whatever now holds it would be wrong.
	if !ok || proc.pid != pid {
		return false, false
	}
	return proc.zombie, true
}
