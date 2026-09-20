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

// The process-group scan has the same zombie problem as the session scan, and
// needs the same answer: `kill(-pgid, 0)` answers true for a process that has
// already exited and is only waiting to be reaped, so a cancelled job whose
// group member died from the cancel's own signal read as abandoned background
// work. That member is completed work, not a survivor -- and a live member
// beside it still is one.
func TestEnvironmentJobProcessGroupScanIgnoresAZombie(t *testing.T) {
	if groupHasLiveMember([]sessionProcess{{pid: 4243, group: 4242, zombie: true}}, 4242) {
		t.Fatalf("a group holding only a zombie must not read as having a survivor")
	}

	procs := []sessionProcess{
		{pid: 4243, group: 4242, zombie: true},
		{pid: 4244, group: 4242},
	}
	if !groupHasLiveMember(procs, 4242) {
		t.Fatalf("a live member alongside a zombie must read as a survivor")
	}
}

// The platform's own table is what the group check consults first, and it is
// consulted exactly where the process group is the question: a table that
// cannot say which group a process is in must not answer instead of the
// fallback.
func TestEnvironmentJobProcessGroupScanIsAskedForTheGroupsOwnMembers(t *testing.T) {
	table := []sessionProcess{
		{pid: 4243, group: 9999},
		{pid: 4244, group: 4242, zombie: true},
	}
	restore := environmentJobSessionProcessesFunc
	environmentJobSessionProcessesFunc = func() ([]sessionProcess, bool) { return table, true }
	t.Cleanup(func() { environmentJobSessionProcessesFunc = restore })

	if alive, ok := platformProcessGroupHasLiveMember(4242); !ok || alive {
		t.Fatalf("a group whose only member is a zombie must answer from the table, as no survivor")
	}
	if alive, ok := platformProcessGroupHasLiveMember(9999); !ok || !alive {
		t.Fatalf("a group with a live member must answer from the table, as a survivor")
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
	if !descendantHasLiveMember(leftover, 4244, nil) {
		t.Fatalf("a live process reparented onto the supervisor must read as a survivor")
	}
	if descendantHasLiveMember(leftover, 4242, nil) {
		t.Fatalf("a process still parented to something else must not read as the supervisor's descendant")
	}

	// The reparented leftover already exited and is only waiting to be reaped.
	completed := []sessionProcess{{pid: 4243, parent: 4244, zombie: true}}
	if descendantHasLiveMember(completed, 4244, nil) {
		t.Fatalf("a reparented zombie must not read as a survivor")
	}

	// A platform that reports no parent must not have its processes attributed
	// to the supervisor, and a supervisor with no pid has nothing to match.
	if descendantHasLiveMember([]sessionProcess{{pid: 4243}}, 4244, nil) {
		t.Fatalf("a process whose parent is unknown must not match any supervisor")
	}
	if descendantHasLiveMember(leftover, 0, nil) {
		t.Fatalf("an unset supervisor pid must not match a process reporting parent zero")
	}

	// A child the supervisor already had before the job started belongs to
	// whatever put it there, not to this job, and must not be reported as this
	// job's leftover -- the case where the supervisor shares a process with a
	// harness that keeps children of its own.
	baseline := map[int]struct{}{4243: {}}
	if descendantHasLiveMember(leftover, 4244, baseline) {
		t.Fatalf("a child that predates the job must not read as a survivor this job left behind")
	}
	// One that arrived after it still must, even with a baseline in hand.
	withNewcomer := append([]sessionProcess{{pid: 4245, parent: 4244}}, leftover...)
	if !descendantHasLiveMember(withNewcomer, 4244, baseline) {
		t.Fatalf("a descendant adopted after the job started must read as a survivor despite the baseline")
	}
}
