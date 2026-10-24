//go:build darwin

package core

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestParsePSProcessTableReadsPidsAndZombies(t *testing.T) {
	procs := parsePSProcessTable([]byte("" +
		"  101     1   101 Ss\n" +
		"  999   101   999 S\n" +
		" 4244   999   999 Z\n" +
		"  oops    1     1 S\n" +
		"  500  oops   500 S\n" +
		"  501     1  oops S\n" +
		"  502     1\n"))
	want := []SessionProcess{
		{Pid: 101, Parent: 1, group: 101},
		{Pid: 999, Parent: 101, group: 999},
		{Pid: 4244, Parent: 999, group: 999, Zombie: true},
	}
	if len(procs) != len(want) {
		t.Fatalf("parsed %d processes, want %d: %+v", len(procs), len(want), procs)
	}
	for index, proc := range procs {
		if proc != want[index] {
			t.Fatalf("process %d parsed as %+v, want %+v", index, proc, want[index])
		}
	}
}

func TestPlatformSessionProcessesReadsThisProcessFromRealPS(t *testing.T) {
	procs, ok := platformSessionProcesses()
	if !ok {
		t.Fatal("platformSessionProcesses could not read the process table")
	}
	group, err := syscall.Getpgid(os.Getpid())
	if err != nil {
		t.Fatalf("getpgid: %v", err)
	}
	session, err := syscall.Getsid(os.Getpid())
	if err != nil {
		t.Fatalf("getsid: %v", err)
	}
	want := SessionProcess{Pid: os.Getpid(), Parent: os.Getppid(), group: group, session: session}
	for _, proc := range procs {
		if proc.Pid == want.Pid {
			if proc != want {
				t.Fatalf("this process read as %+v, want %+v", proc, want)
			}
			return
		}
	}
	t.Fatalf("this process (pid %d) is missing from %d parsed ps rows", want.Pid, len(procs))
}

// Exercises the real Darwin table, whose ps session column is 0 for every process.
func TestDarwinSessionScanFindsBackgroundedWorkInAFreshProcessGroup(t *testing.T) {
	backgroundLog := filepath.Join(t.TempDir(), "background.log")
	cmd := exec.Command("bash", "-c", fmt.Sprintf("set -m; sleep 5 </dev/null >%s 2>&1 & exit 0", backgroundLog))
	DetachEnvironmentJobChild(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { killProcessesMatching(backgroundLog) })
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}

	if !environmentJobSessionHasLiveMember(pid) {
		t.Fatalf("session %d still holds the backgrounded process, but the scan reported no live member", pid)
	}
}

func TestDarwinSessionScanSeesNoMemberInACleanJob(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	DetachEnvironmentJobChild(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}

	if environmentJobSessionHasLiveMember(pid) {
		t.Fatalf("session %d holds nothing but its reaped leader, but the scan reported a live member", pid)
	}
}
