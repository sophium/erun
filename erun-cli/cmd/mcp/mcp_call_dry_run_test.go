package mcp

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/mcp"
)

// The reattach spawn outlives the preview, so assert it was never invoked rather than reading the process table.
func TestCallMCPToolWithReattachDryRunNeverReattaches(t *testing.T) {
	seedDesktopIdentityForTest(t)

	var spawned []string
	restore := swapReattachMCPChannel(func(_ core.Context, tenant, environment string) error {
		spawned = append(spawned, tenant+"/"+environment)
		return nil
	})
	t.Cleanup(restore)

	port := closedLoopbackPort(t)
	target := shared.McpEdgeTarget{
		Tenant:      "team",
		Environment: "dev",
		Port:        port,
		Endpoint:    mcp.MCPLocalEndpoint(port),
	}

	_, err := shared.CallMCPToolWithReattach(context.Background(), testCommandContext(t, true), target, "whip", map[string]any{"preview": true}, false)
	if !errors.Is(err, mcp.ErrMCPEndpointUnreachable) {
		t.Fatalf("dry run against a closed port returned %v, want an unreachable endpoint", err)
	}
	if len(spawned) != 0 {
		t.Fatalf("--dry-run reattached %v, want no reattach at all", spawned)
	}

	_, _ = shared.CallMCPToolWithReattach(context.Background(), testCommandContext(t, false), target, "whip", map[string]any{}, false)
	if len(spawned) != 1 || spawned[0] != "team/dev" {
		t.Fatalf("a real run reattached %v, want exactly team/dev", spawned)
	}
}

func testCommandContext(t *testing.T, dryRun bool) core.Context {
	t.Helper()
	var out bytes.Buffer
	return core.Context{Logger: core.NewLoggerWithWriters(0, &out, &out), DryRun: dryRun}
}

func swapReattachMCPChannel(replacement func(core.Context, string, string) error) func() {
	original := shared.ReattachMCPChannel
	shared.ReattachMCPChannel = replacement
	return func() { shared.ReattachMCPChannel = original }
}

func closedLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a loopback port: %v", err)
	}
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("loopback listener address is %T, not TCP", listener.Addr())
	}
	port := addr.Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release loopback port %d: %v", port, err)
	}
	return port
}

// seedDesktopIdentityForTest lets bearer minting succeed so the call reaches the dial.
func seedDesktopIdentityForTest(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", os.Getenv("XDG_CONFIG_HOME"))

	dir := core.DefaultDesktopIdentityDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatalf("marshal identity: %v", err)
	}
	key := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(filepath.Join(dir, "desktopid.key"), key, 0o600); err != nil {
		t.Fatalf("write identity: %v", err)
	}
}
