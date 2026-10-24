package build

import (
	"os/exec"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/infra/kube"
)

// The stub writes well over 64KB after the forwarding line; a parent that stops draining hangs Wait.
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

	result := make(chan kube.PortForwardResult, 1)
	go kube.DrainPortForwardOutput(stdout, result)

	select {
	case found := <-result:
		if found.Err != nil {
			t.Fatalf("unexpected error finding the port: %v", found.Err)
		}
		if found.Port != 38471 {
			t.Errorf("port = %d, want 38471", found.Port)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the forwarding port to be reported")
	}

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
