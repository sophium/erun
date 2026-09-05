package eruncommon

import (
	"os/exec"
	"testing"
	"time"
)

// TestDrainPortForwardOutputKeepsDrainingAfterReportingThePort reproduces a
// parent-stops-draining bug one hop down the stack from the docker
// build/push deadlock this same shape was found in: a kubectl port-forward
// the build/push path keeps open for as long as a registry is reachable
// through it. The old readForwardingPort returned from its scan loop the
// moment it found the "Forwarding from ..." line, so nothing ever read
// kubectl's stdout again for the rest of the forward's life -- once kubectl
// wrote more than one pipe buffer's worth of later "Handling connection for
// ..." lines, it would block on write forever and the tunnel it was providing
// would stall silently.
//
// The stub here writes the forwarding line and then well over 64KB more, so a
// parent that stops draining after the first line leaves the child blocked
// and cmd.Wait() never returns.
func TestDrainPortForwardOutputKeepsDrainingAfterReportingThePort(t *testing.T) {
	script := `echo "Forwarding from 127.0.0.1:38471 -> 80"
for i in $(seq 1 20000); do
  echo "Handling connection for 38471 padding padding padding padding padding padding"
done`
	stub := writeExecutableScript(t, script)

	cmd := exec.Command(stub)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	result := make(chan portForwardResult, 1)
	go drainPortForwardOutput(stdout, result)

	select {
	case found := <-result:
		if found.err != nil {
			t.Fatalf("unexpected error finding the port: %v", found.err)
		}
		if found.port != 38471 {
			t.Errorf("port = %d, want 38471", found.port)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the forwarding port to be reported")
	}

	// The real regression: with the port already reported, does the rest of
	// the child's output still get drained, or does the child block on write
	// and cmd.Wait() hang forever?
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	select {
	case err := <-waitErr:
		if err != nil {
			t.Fatalf("cmd.Wait: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("hung: child never exited -- stdout was not drained after the port line")
	}
}
