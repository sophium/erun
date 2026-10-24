//go:build linux

package core

import (
	"os"
	"strconv"
)

// PlatformProcessZombie reports whether pid is a zombie, from /proc/<pid>/stat.
// The second return is false when that cannot be determined.
func PlatformProcessZombie(pid int) (bool, bool) {
	if pid <= 0 {
		return false, false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false, false
	}
	proc, ok := parseProcStat(stat)
	// The pid may have been reused since the read started.
	if !ok || proc.Pid != pid {
		return false, false
	}
	return proc.Zombie, true
}
