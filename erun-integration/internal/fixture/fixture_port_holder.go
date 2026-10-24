package fixture

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-integration/internal/harnessexec"
)

var (
	portSimBuildOnce sync.Once
	portSimPath      string
	errPortSimBuild  error
)

// PortSimBinary builds (once per process) a small Go program that listens on
// a TCP port until killed. Used as the body of a `kubectl port-forward` stub
// so production code's "is the local port reachable?" polling can succeed in
// integration tests without standing up a real cluster.
func PortSimBinary(t testing.TB) string {
	t.Helper()
	portSimBuildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "erun-portsim-*")
		if err != nil {
			errPortSimBuild = fmt.Errorf("mkdir portsim cache: %w", err)
			return
		}
		out := filepath.Join(dir, "portsim")
		_, thisFile, _, ok := runtime.Caller(0)
		if !ok {
			errPortSimBuild = fmt.Errorf("resolve fixture package path")
			return
		}
		pkgDir := filepath.Join(filepath.Dir(thisFile), "portsim")
		cmd := harnessexec.Command("go", "build", "-o", out, ".")
		cmd.Dir = pkgDir
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if combined, err := cmd.CombinedOutput(); err != nil {
			errPortSimBuild = fmt.Errorf("build portsim: %w: %s", err, combined)
			return
		}
		portSimPath = out
	})
	if errPortSimBuild != nil {
		t.Fatalf("%v", errPortSimBuild)
	}
	return portSimPath
}

// StartStalePortHolder starts a listener that binds a local port and never
// answers anything through it — the forward whose target pod was replaced. It
// returns the holder's real PID so a scenario can present it to erun's
// adopt-or-replace probes and then assert that erun actually stopped it; a
// fabricated PID must never be used for that, because production kills what the
// probe names.
func StartStalePortHolder(t testing.TB, port int) int {
	t.Helper()
	return startPortHolder(t, port, "--silent")
}

// StartServingPortHolder starts a listener that answers what erun's
// reachability probe asks — the working forward a scenario wants erun to adopt
// rather than replace. Adoption now depends on the tunnel carrying traffic, so
// a scenario that only claims a holder through lsof would be deciding on
// whatever else happens to hold that port on the host.
func StartServingPortHolder(t testing.TB, port int) int {
	t.Helper()
	return startPortHolder(t, port)
}

func startPortHolder(t testing.TB, port int, extra ...string) int {
	t.Helper()
	cmd := harnessexec.Command(PortSimBinary(t), append([]string{"--port", strconv.Itoa(port)}, extra...)...)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start port holder on %d: %v", port, err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return pid
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("port holder never bound 127.0.0.1:%d", port)
	return 0
}

// StalePortHolderStopped reports whether erun stopped the holder, waiting for
// the port to come free rather than for a fixed delay.
func StalePortHolderStopped(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 100*time.Millisecond)
		if err != nil {
			return true
		}
		_ = conn.Close()
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// StartUnboundPortForwardProcess starts a live process that never binds any
// port — the shape of a `kubectl port-forward` that is still retrying
// against a pod that never answers, so its state-file PID is alive with
// nothing to show for it. It exists to test the reap of a recorded forward
// that never got as far as binding, as opposed to StartStalePortHolder's
// bound-but-dead shape.
func StartUnboundPortForwardProcess(t testing.TB) int {
	t.Helper()
	cmd := harnessexec.Command(PortSimBinary(t), "--no-listen")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start unbound port-forward process: %v", err)
	}
	pid := cmd.Process.Pid
	// A killed process stays a zombie — still "alive" to signal(0) — until
	// something calls Wait on it. Reaping it here, the moment it exits,
	// rather than only in t.Cleanup (which does not run until the whole test
	// body has returned) is what lets ProcessStopped's liveness probe below
	// observe production's kill promptly instead of only at test teardown.
	reaped := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(reaped)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-reaped
	})
	return pid
}

// ProcessStopped reports whether erun killed the process at pid, waiting for
// the OS to actually reap it rather than for a fixed delay. Unlike a bound
// port's holder, a never-bound forward leaves no listener to dial, so
// liveness here goes through the real process table (see
// core.DesktopProcessAlive) instead of a network probe.
func ProcessStopped(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !core.DesktopProcessAlive(pid) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
