//go:build !windows

package eruncommon

import "testing"

// darwinPSSessionOutput is `ps -axo pid=,sess=,stat=` as Darwin prints it: the
// pid and state columns are sound, and sess is 0 for every process because the
// kernel's user-visible kinfo_proc has nowhere to carry a session id. It is the
// table a session scan used to read, and pid 4243 — work the job's own child
// (pid 4242, session leader) backgrounded into a fresh process group — does not
// appear in it as a member of session 4242 at all.
const darwinPSSessionOutput = `  101       0 Ss
  999       0 S
 4242       0 Ss
 4243       0 S
 4244       0 Z
`

// A job that backgrounds its work into a fresh process group is the survivor
// shape environmentJobProcessGroupSurvivors cannot see, and on Darwin the
// session check could not see it either: the session column ps reports is
// empty there, so the scan found no member, the job recorded Succeeded, and the
// work it left behind kept running. The kernel does know the session — it is
// just not in that column — so the Darwin table is built from getsid(2)
// instead, which is the table this exercises through the seam.
func TestEnvironmentJobSessionScanFindsTheMemberDarwinPsCannotReport(t *testing.T) {
	if parsePSSessionTable([]byte(darwinPSSessionOutput), 4242) {
		t.Fatalf("the fixture no longer reproduces Darwin's empty session column: ps reported a member of session 4242")
	}

	table := []sessionProcess{
		{pid: 101, session: 101},
		{pid: 999, session: 999},
		{pid: 4242, session: 4242},
		{pid: 4243, session: 4242},
		{pid: 4244, session: 4242, zombie: true},
	}
	restore := environmentJobSessionProcessesFunc
	environmentJobSessionProcessesFunc = func() ([]sessionProcess, bool) { return table, true }
	t.Cleanup(func() { environmentJobSessionProcessesFunc = restore })

	if !environmentJobSessionHasLiveMember(4242) {
		t.Fatalf("a live process in the job's session must read as a survivor even though ps cannot name the session")
	}
	if environmentJobSessionHasLiveMember(101) {
		t.Fatalf("a session whose only member is its own leader must not read as having a survivor")
	}
}

// The zombie handling the ps-based check had must survive the move to a
// platform process table: an exited process waiting to be reaped is completed
// work, and the job's own tracked child has already been reaped by the time
// the scan runs, so neither may read as a survivor.
func TestEnvironmentJobSessionScanIgnoresAZombieAndTheLeader(t *testing.T) {
	completed := []sessionProcess{
		{pid: 4242, session: 4242},
		{pid: 4243, session: 4242, zombie: true},
	}
	if sessionHasLiveMember(completed, 4242) {
		t.Fatalf("a session holding only its reaped leader and a zombie must not read as having a survivor")
	}

	procs := append([]sessionProcess{{pid: 4244, session: 4242}}, completed...)
	if !sessionHasLiveMember(procs, 4242) {
		t.Fatalf("a live member alongside a zombie must read as a survivor")
	}
}

// A platform that cannot report a session for a process contributes no member
// for it, rather than a member of session zero.
func TestEnvironmentJobSessionScanSkipsProcessesWithNoReportedSession(t *testing.T) {
	if sessionHasLiveMember([]sessionProcess{{pid: 4243}}, 4242) {
		t.Fatalf("a process whose session is unknown must not match any session")
	}
}

// The descendant scan is the one that catches a leftover which called setsid
// for itself: it is in a session and a process group of its own, so neither
// scan above can name it, and its parent is the supervisor it was handed to.
// A zombie is still completed work rather than a survivor, and a row the
// platform could not report a parent for must not be attributed to pid zero.
func TestEnvironmentJobDescendantScanFindsTheReparentedLeftover(t *testing.T) {
	leftover := []sessionProcess{
		{pid: 4242, parent: 1},
		{pid: 4243, parent: 4244, session: 4243},
	}
	if !descendantHasLiveMember(leftover, 4244) {
		t.Fatalf("a live process reparented onto the supervisor must read as a survivor")
	}
	if descendantHasLiveMember(leftover, 4242) {
		t.Fatalf("a process still parented to something else must not read as the supervisor's descendant")
	}

	// The reparented leftover already exited and is only waiting to be reaped.
	completed := []sessionProcess{{pid: 4243, parent: 4244, zombie: true}}
	if descendantHasLiveMember(completed, 4244) {
		t.Fatalf("a reparented zombie must not read as a survivor")
	}

	// A platform that reports no parent must not have its processes attributed
	// to the supervisor, and a supervisor with no pid has nothing to match.
	if descendantHasLiveMember([]sessionProcess{{pid: 4243}}, 4244) {
		t.Fatalf("a process whose parent is unknown must not match any supervisor")
	}
	if descendantHasLiveMember(leftover, 0) {
		t.Fatalf("an unset supervisor pid must not match a process reporting parent zero")
	}
}
