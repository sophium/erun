//go:build !windows

package core

import (
	"testing"
)

// darwinPSSessionOutput is Darwin's `ps -axo pid=,sess=,stat=`: sess is 0 for
// every process, so backgrounded pid 4243 is not visible as a session member.
const darwinPSSessionOutput = `  101       0 Ss
  999       0 S
 4242       0 Ss
 4243       0 S
 4244       0 Z
`

func TestEnvironmentJobSessionScanFindsTheMemberDarwinPsCannotReport(t *testing.T) {
	if parsePSSessionTable([]byte(darwinPSSessionOutput), 4242) {
		t.Fatalf("the fixture no longer reproduces Darwin's empty session column: ps reported a member of session 4242")
	}

	table := []SessionProcess{
		{Pid: 101, session: 101},
		{Pid: 999, session: 999},
		{Pid: 4242, session: 4242},
		{Pid: 4243, session: 4242},
		{Pid: 4244, session: 4242, Zombie: true},
	}
	restore := EnvironmentJobSessionProcessesFunc
	EnvironmentJobSessionProcessesFunc = func() ([]SessionProcess, bool) { return table, true }
	t.Cleanup(func() { EnvironmentJobSessionProcessesFunc = restore })

	if !environmentJobSessionHasLiveMember(4242) {
		t.Fatalf("a live process in the job's session must read as a survivor even though ps cannot name the session")
	}
	if environmentJobSessionHasLiveMember(101) {
		t.Fatalf("a session whose only member is its own leader must not read as having a survivor")
	}
}

func TestEnvironmentJobSessionScanIgnoresAZombieAndTheLeader(t *testing.T) {
	completed := []SessionProcess{
		{Pid: 4242, session: 4242},
		{Pid: 4243, session: 4242, Zombie: true},
	}
	if sessionHasLiveMember(completed, 4242) {
		t.Fatalf("a session holding only its reaped leader and a zombie must not read as having a survivor")
	}

	procs := append([]SessionProcess{{Pid: 4244, session: 4242}}, completed...)
	if !sessionHasLiveMember(procs, 4242) {
		t.Fatalf("a live member alongside a zombie must read as a survivor")
	}
}

func TestEnvironmentJobSessionScanSkipsProcessesWithNoReportedSession(t *testing.T) {
	if sessionHasLiveMember([]SessionProcess{{Pid: 4243}}, 4242) {
		t.Fatalf("a process whose session is unknown must not match any session")
	}
}

func TestEnvironmentJobProcessGroupScanIgnoresAZombie(t *testing.T) {
	if groupHasLiveMember([]SessionProcess{{Pid: 4243, group: 4242, Zombie: true}}, 4242) {
		t.Fatalf("a group holding only a zombie must not read as having a survivor")
	}

	procs := []SessionProcess{
		{Pid: 4243, group: 4242, Zombie: true},
		{Pid: 4244, group: 4242},
	}
	if !groupHasLiveMember(procs, 4242) {
		t.Fatalf("a live member alongside a zombie must read as a survivor")
	}
}

func TestEnvironmentJobProcessGroupScanIsAskedForTheGroupsOwnMembers(t *testing.T) {
	table := []SessionProcess{
		{Pid: 4243, group: 9999},
		{Pid: 4244, group: 4242, Zombie: true},
	}
	restore := EnvironmentJobSessionProcessesFunc
	EnvironmentJobSessionProcessesFunc = func() ([]SessionProcess, bool) { return table, true }
	t.Cleanup(func() { EnvironmentJobSessionProcessesFunc = restore })

	if alive, ok := platformProcessGroupHasLiveMember(4242); !ok || alive {
		t.Fatalf("a group whose only member is a zombie must answer from the table, as no survivor")
	}
	if alive, ok := platformProcessGroupHasLiveMember(9999); !ok || !alive {
		t.Fatalf("a group with a live member must answer from the table, as a survivor")
	}
}

func TestEnvironmentJobDescendantScanFindsTheReparentedLeftover(t *testing.T) {
	leftover := []SessionProcess{
		{Pid: 4242, Parent: 1},
		{Pid: 4243, Parent: 4244, session: 4243},
	}
	if !descendantHasLiveMember(leftover, 4244, nil) {
		t.Fatalf("a live process reparented onto the supervisor must read as a survivor")
	}
	if descendantHasLiveMember(leftover, 4242, nil) {
		t.Fatalf("a process still parented to something else must not read as the supervisor's descendant")
	}

	completed := []SessionProcess{{Pid: 4243, Parent: 4244, Zombie: true}}
	if descendantHasLiveMember(completed, 4244, nil) {
		t.Fatalf("a reparented zombie must not read as a survivor")
	}

	if descendantHasLiveMember([]SessionProcess{{Pid: 4243}}, 4244, nil) {
		t.Fatalf("a process whose parent is unknown must not match any supervisor")
	}
	if descendantHasLiveMember(leftover, 0, nil) {
		t.Fatalf("an unset supervisor pid must not match a process reporting parent zero")
	}

	// A child that predates the job is not this job's leftover.
	baseline := map[int]struct{}{4243: {}}
	if descendantHasLiveMember(leftover, 4244, baseline) {
		t.Fatalf("a child that predates the job must not read as a survivor this job left behind")
	}
	withNewcomer := append([]SessionProcess{{Pid: 4245, Parent: 4244}}, leftover...)
	if !descendantHasLiveMember(withNewcomer, 4244, baseline) {
		t.Fatalf("a descendant adopted after the job started must read as a survivor despite the baseline")
	}
}
