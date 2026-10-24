package deploy

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// helm writes the terminal status from its own SIGINT handler, so a hard kill
// leaves the release locked in pending-upgrade. Exit 7 comes only from the trap.
func TestInterruptHelmDeployGraceLetsHelmReleaseItsOwnLock(t *testing.T) {
	cmd, helmDone := startInterruptibleChild(t, `trap 'exit 7' INT`)

	started := time.Now()
	err := interruptHelmDeploy(cmd, helmDone, 20*time.Second)
	elapsed := time.Since(started)

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("expected the child to run its own interrupt handler and exit 7, got %v", err)
	}
	if elapsed >= 20*time.Second {
		t.Fatalf("expected the graceful exit well inside the grace, took %s", elapsed)
	}
}

func TestInterruptHelmDeployKillsAHelmThatIgnoresItsSignal(t *testing.T) {
	cmd, helmDone := startInterruptibleChild(t, `trap '' INT`)

	err := interruptHelmDeploy(cmd, helmDone, 300*time.Millisecond)

	if err == nil {
		t.Fatal("expected a killed helm to report its failure")
	}
	if !strings.Contains(err.Error(), "killed") || strings.Contains(err.Error(), "interrupt") {
		t.Fatalf("expected the child to be killed after the grace, got %v", err)
	}
}

// The readiness marker is a file, not a pipe: a grandchild holding a pipe's
// write end would block cmd.Wait.
func startInterruptibleChild(t *testing.T, handler string) (*exec.Cmd, <-chan error) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "ready")
	script := handler + `; printf ready > ` + marker + `; while true; do sleep 0.1; done`
	cmd := exec.Command("sh", "-c", script)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("the child never reported that its interrupt handler was installed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	helmDone := make(chan error, 1)
	go func() { helmDone <- cmd.Wait() }()
	return cmd, helmDone
}
