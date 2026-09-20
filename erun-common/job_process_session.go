//go:build !windows

package eruncommon

import "strings"

// sessionProcess is one process as a session scan sees it: the pid, whether it
// is a zombie (already exited, only waiting to be reaped), the session it
// belongs to, and the parent it currently reports. A zero session means the
// platform could not report one for that process, which is not the same as
// session zero -- no process is in it. A zero parent likewise means the
// platform could not report one, which is not the same as a parent of pid 0.
type sessionProcess struct {
	pid     int
	zombie  bool
	session int
	parent  int
}

// environmentJobSessionProcessesFunc builds the table
// environmentJobSessionHasLiveMember scans; a var, matching
// environmentJobResourceStateSummaryFunc's established test-seam shape in
// job.go, so a test can substitute a table instead of depending on whatever
// happens to be running beside it. That substitution is also how the one table
// no host here can produce — Darwin's, where ps leaves the session column
// empty — gets exercised on a machine that is not Darwin.
var environmentJobSessionProcessesFunc = platformSessionProcesses

// sessionHasLiveMember reports whether the table holds a live member of
// session sid other than its leader. The leader is the job's own tracked
// child, already reaped by the time this is asked; a zombie is completed work
// nobody has reaped yet, not abandoned background work, exactly as
// environmentJobProcessGroupHasLiveMember treats it.
func sessionHasLiveMember(procs []sessionProcess, sid int) bool {
	for _, proc := range procs {
		if proc.pid == sid || proc.zombie || proc.session != sid {
			continue
		}
		return true
	}
	return false
}

// descendantHasLiveMember reports whether the table holds a live, non-zombie
// process whose parent is parentPID and which was not already there when the
// job started. It is what catches work that escaped both the process group and
// the session: a descendant that called setsid itself gets a fresh group *and*
// a fresh session, so neither of the scans above can name it, but it cannot
// escape being reparented -- when the process that spawned it exits, the
// kernel hands it to the nearest ancestor marked as a child subreaper, which
// is this supervisor (see enableEnvironmentJobSubreaper). Its parent is then
// the supervisor itself, whatever it did to its own group and session.
//
// baseline is the set of pids already parented to the supervisor when it began
// the work. On a supervisor's own process that set is empty, since nothing
// else runs there; it is non-empty where the supervisor shares a process with
// something else that keeps children of its own, and those are not this job's
// to report.
//
// A zombie is excluded for the same reason the scans above exclude it:
// completed work nobody has reaped yet is not abandoned background work. A
// zero parent marks a row the platform could not answer for, and a negative
// parentPID means there is no supervisor pid to compare against, so neither
// can match.
func descendantHasLiveMember(procs []sessionProcess, parentPID int, baseline map[int]struct{}) bool {
	if parentPID <= 0 {
		return false
	}
	for _, proc := range procs {
		if proc.zombie || proc.parent != parentPID {
			continue
		}
		if _, existed := baseline[proc.pid]; existed {
			continue
		}
		return true
	}
	return false
}

// psStatIsZombie answers whether a ps STAT column names a zombie. The letters
// that follow Z describe the same process, so this is a containment test.
func psStatIsZombie(stat string) bool {
	return strings.Contains(stat, "Z")
}
