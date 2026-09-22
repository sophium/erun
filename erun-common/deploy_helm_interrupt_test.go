package eruncommon

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestInterruptHelmDeployGraceLetsHelmReleaseItsOwnLock is the reproduction for
// the release a failed rollout left locked: the pod watcher's abort path sent
// SIGINT and then killed helm's process tree two seconds later whatever helm
// was doing. helm records pending-upgrade before it applies anything and its
// own signal handler is what writes the terminal status back, so a helm killed
// outright strands the release in a state that holds helm's lock, and every
// later deploy of that environment fails against it until the metadata is
// cleared by hand. Verified against real helm: SIGINT released the lock and
// recorded STATUS: failed, while a hard kill left STATUS: pending-upgrade.
//
// The child here stands in for that signal handler: the exit status it reports
// is one only its own trap can produce, so a returned 7 proves helm was given
// the chance to finish its shutdown rather than killed.
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

// TestInterruptHelmDeployKillsAHelmThatIgnoresItsSignal is the other half: the
// grace is a bound, not a wait for helm to agree. A helm that does not exit has
// to be killed so the abort the watcher asked for actually happens, and the
// caller must see that kill rather than a nil error.
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

// startInterruptibleChild runs a child that installs handler before signalling
// readiness, and returns it with the channel cmd.Wait reports on. The readiness
// marker is a file rather than a pipe read: a killed shell whose grandchild
// still holds a pipe's write end would block the wait, which is the hazard the
// integration harness's harnessexec exists for and one this test has no reason
// to reproduce. Waiting for the marker is what makes the signal arrive after
// the handler is installed -- sending it earlier would exercise a shell that
// has no handler yet, which is neither case under test.
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
