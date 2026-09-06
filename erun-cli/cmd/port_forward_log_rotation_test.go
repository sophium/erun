package cmd

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	common "github.com/sophium/erun/erun-common"
)

// listenOnFreeLocalPort binds a real listener so canConnectLocalPort (which
// dials, not stubs) observes the port as bound -- the same observation a live
// kubectl port-forward would produce.
func listenOnFreeLocalPort(t *testing.T) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on a free local port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	return port, func() { _ = ln.Close() }
}

func writeOversizedFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatalf("seed oversized log: %v", err)
	}
}

// TestReusableRecordedPortForwardRotatesOversizedLogWhenServing is the
// regression test for erun#2161 on the mcp/api reuse path: a forward that
// is found alive and serving on every touch (every `erun open` for that env)
// must reclaim an already-oversized log rather than leaving it to grow
// forever, since a serving forward is never restarted and its log's fd is
// never reopened. Fails without the rotation call wired into
// reusableRecordedPortForward's PortForwardServing branch.
func TestReusableRecordedPortForwardRotatesOversizedLogWhenServing(t *testing.T) {
	port, closeListener := listenOnFreeLocalPort(t)
	defer closeListener()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "mcp.log")
	writeOversizedFile(t, logPath, int(common.PortForwardLogMaxBytes)+1)

	state := mcpPortForwardState{Tenant: "acme", Environment: "dev", LocalPort: port}
	ctx := common.Context{Logger: common.NewLoggerWithWriters(0, os.Stderr, os.Stderr)}

	ok := reusableRecordedPortForward(ctx, "mcp", logPath, state, state, port, func(int) bool { return true })
	if !ok {
		t.Fatalf("expected a bound, traffic-carrying forward to be reused")
	}

	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat log: %v", err)
	}
	if info.Size() > common.PortForwardLogMaxBytes {
		t.Fatalf("expected the log rotated back under the cap, got %d bytes", info.Size())
	}
	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Fatalf("expected a rotated backup, stat err = %v", err)
	}
}

// TestReusableRecordedPortForwardLeavesASmallLogUntouched pins the negative
// case: a healthy forward whose log is already under the cap must not be
// rotated on every reuse.
func TestReusableRecordedPortForwardLeavesASmallLogUntouched(t *testing.T) {
	port, closeListener := listenOnFreeLocalPort(t)
	defer closeListener()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "mcp.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("small"), 0o644); err != nil {
		t.Fatalf("seed small log: %v", err)
	}

	state := mcpPortForwardState{Tenant: "acme", Environment: "dev", LocalPort: port}
	ctx := common.Context{Logger: common.NewLoggerWithWriters(0, os.Stderr, os.Stderr)}

	ok := reusableRecordedPortForward(ctx, "mcp", logPath, state, state, port, func(int) bool { return true })
	if !ok {
		t.Fatalf("expected a bound, traffic-carrying forward to be reused")
	}
	if _, err := os.Stat(logPath + ".1"); !os.IsNotExist(err) {
		t.Fatalf("expected no backup for a log already under the cap, stat err = %v", err)
	}
}

// fakeSSHDListener binds a real listener that answers the "SSH-" banner
// prefix canReachLocalSSHEndpoint checks for, so ensureSSHDPortForward's
// reuse check observes a genuinely serving forward.
func fakeSSHDListener(t *testing.T) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on a free local port: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("SSH-2.0-fake\r\n"))
			_ = conn.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	return port, func() { _ = ln.Close() }
}

// TestEnsureSSHDPortForwardRotatesOversizedLogWhenServing mirrors the mcp/api
// coverage above for the sshd forward's own inline reuse check, which does
// not go through reusableRecordedPortForward: a forward found alive and
// serving on every touch must reclaim an already-oversized log, since a
// serving forward is never restarted and its log's fd is never reopened.
func TestEnsureSSHDPortForwardRotatesOversizedLogWhenServing(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	port, closeListener := fakeSSHDListener(t)
	defer closeListener()

	result := common.OpenResult{
		Tenant:      "acme",
		Environment: "dev",
		EnvConfig:   common.EnvConfig{SSHD: common.SSHDConfig{LocalPort: port}},
	}

	statePath, err := sshdPortForwardStatePath(result.Tenant, result.Environment, false)
	if err != nil {
		t.Fatalf("resolve state path: %v", err)
	}
	logPath := sshdPortForwardLogPath(statePath)
	writeOversizedFile(t, logPath, int(common.PortForwardLogMaxBytes)+1)

	namespace := common.KubernetesNamespaceName(result.Tenant, result.Environment)
	seeded := sshdPortForwardState{
		Tenant:      result.Tenant,
		Environment: result.Environment,
		Namespace:   namespace,
		LocalPort:   port,
	}
	if err := saveSSHDPortForwardState(statePath, seeded); err != nil {
		t.Fatalf("seed matching state: %v", err)
	}

	ctx := common.Context{Logger: common.NewLoggerWithWriters(0, os.Stderr, os.Stderr)}
	info, err := ensureSSHDPortForward(ctx, result)
	if err != nil {
		t.Fatalf("ensureSSHDPortForward: %v", err)
	}
	if info.Port != port {
		t.Fatalf("expected the existing serving forward to be reused on port %d, got %d", port, info.Port)
	}

	logInfo, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat log: %v", err)
	}
	if logInfo.Size() > common.PortForwardLogMaxBytes {
		t.Fatalf("expected the log rotated back under the cap, got %d bytes", logInfo.Size())
	}
	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Fatalf("expected a rotated backup, stat err = %v", err)
	}
}
