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

// PortSimBinary builds (once per process) a TCP listener used as a fake
// `kubectl port-forward` body.
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

// StartStalePortHolder starts a listener that binds port but never answers and
// returns its real PID, since production kills the PID its probe names.
func StartStalePortHolder(t testing.TB, port int) int {
	t.Helper()
	return startPortHolder(t, port, "--silent")
}

// StartServingPortHolder starts a listener that answers erun's reachability
// probe, i.e. a forward erun should adopt.
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

// StalePortHolderStopped reports whether the port came free within timeout.
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

// StartUnboundPortForwardProcess starts a live process that never binds a port,
// like a port-forward still retrying against an unresponsive pod.
func StartUnboundPortForwardProcess(t testing.TB) int {
	t.Helper()
	cmd := harnessexec.Command(PortSimBinary(t), "--no-listen")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start unbound port-forward process: %v", err)
	}
	pid := cmd.Process.Pid
	// Reap on exit, not only at cleanup: a zombie still looks alive to
	// ProcessStopped.
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

// ProcessStopped reports whether the process at pid exited within timeout.
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
