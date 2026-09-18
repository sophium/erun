//go:build !windows

package eruncommon

import "strings"

// sessionProcess is one process as a session scan sees it: the pid, whether it
// is a zombie (already exited, only waiting to be reaped), and the session it
// belongs to. A zero session means the platform could not report one for that
// process, which is not the same as session zero -- no process is in it.
type sessionProcess struct {
	pid     int
	zombie  bool
	session int
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

// psStatIsZombie answers whether a ps STAT column names a zombie. The letters
// that follow Z describe the same process, so this is a containment test.
func psStatIsZombie(stat string) bool {
	return strings.Contains(stat, "Z")
}
