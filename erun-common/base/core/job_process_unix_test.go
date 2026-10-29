//go:build !windows

package core

import (
	"os/exec"
	"testing"
	"time"
)

// A zombie left in the group must not count as a survivor.
func TestEnvironmentJobProcessGroupSurvivorsIgnoresAZombie(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	DetachEnvironmentJobChild(cmd)
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

	if EnvironmentJobProcessGroupSurvivors(pid) {
		t.Fatalf("a zombie-only process group must not read as having a survivor")
	}
}

func TestEnvironmentJobProcessGroupSurvivorsDetectsALiveProcess(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	DetachEnvironmentJobChild(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = SignalEnvironmentJobProcessGroup(pid, "KILL")
		_ = cmd.Wait()
	})

	if !EnvironmentJobProcessGroupSurvivors(pid) {
		t.Fatalf("a live process in the group must read as a survivor")
	}
}
