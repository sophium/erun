//go:build !windows

package core

import (
	"strings"
)

// SessionProcess is one process row of a session scan. Zero Session or Parent
// means the platform could not report it.
type SessionProcess struct {
	Pid     int
	Zombie  bool
	session int
	group   int
	Parent  int
}

// EnvironmentJobSessionProcessesFunc is a test seam; it also lets the Darwin
// table shape be exercised on other hosts.
var EnvironmentJobSessionProcessesFunc = platformSessionProcesses

// sessionHasLiveMember reports a live non-leader member of session sid; zombies are completed work.
func sessionHasLiveMember(procs []SessionProcess, sid int) bool {
	for _, proc := range procs {
		if proc.Pid == sid || proc.Zombie || proc.session != sid {
			continue
		}
		return true
	}
	return false
}

// groupHasLiveMember is the process-group variant: unlike kill(-pgid, 0) it does
// not count zombies. The leader is not excluded; it is already reaped when asked.
func groupHasLiveMember(procs []SessionProcess, pgid int) bool {
	for _, proc := range procs {
		if proc.Zombie || proc.group != pgid {
			continue
		}
		return true
	}
	return false
}

// descendantHasLiveMember reports a live child of parentPID not in baseline. It
// catches work that called setsid, which still reparents to this subreaper.
// Zombies, zero parents, and a negative parentPID never match.
func descendantHasLiveMember(procs []SessionProcess, parentPID int, baseline map[int]struct{}) bool {
	if parentPID <= 0 {
		return false
	}
	for _, proc := range procs {
		if proc.Zombie || proc.Parent != parentPID {
			continue
		}
		if _, existed := baseline[proc.Pid]; existed {
			continue
		}
		return true
	}
	return false
}

// psStatIsZombie reports whether a ps STAT column contains Z.
func psStatIsZombie(stat string) bool {
	return strings.Contains(stat, "Z")
}
