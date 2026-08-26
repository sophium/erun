//go:build !windows

package eruncommon

import (
	"os/exec"
	"testing"
	"time"
)

// A cancelled job's whole process group can contain a zombie -- a process
// that already exited and is only waiting for its parent to reap it -- left
// behind when an intermediate wrapper the job's command ran dies before it
// can wait() on its own child. environmentJobProcessGroupSurvivors must read
// that as "nothing left running", not as a survivor: a real regression this
// pins (erun-integration's job cancel scenario caught a first version of this
// check answering true for a zombie, misreporting a clean cancel as
// abandoned work).
func TestEnvironmentJobProcessGroupSurvivorsIgnoresAZombie(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	detachEnvironmentJobChild(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = cmd.Wait() })

	deadline := time.Now().Add(5 * time.Second)
	for {
		if live, ok := psProcessGroupHasLiveMember(pid); ok && !live {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("process %d never became a reapable zombie", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if environmentJobProcessGroupSurvivors(pid) {
		t.Fatalf("a zombie-only process group must not read as having a survivor")
	}
}

func TestEnvironmentJobProcessGroupSurvivorsDetectsALiveProcess(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	detachEnvironmentJobChild(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = signalEnvironmentJobProcessGroup(pid, "KILL")
		_ = cmd.Wait()
	})

	if !environmentJobProcessGroupSurvivors(pid) {
		t.Fatalf("a live process in the group must read as a survivor")
	}
}
