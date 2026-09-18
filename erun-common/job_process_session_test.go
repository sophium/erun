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
