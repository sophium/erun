package cmd

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

	common "github.com/sophium/erun/erun-common"
)

// TestCallMCPToolWithReattachDryRunNeverReattaches locks the preview contract on
// the shared reattach choke point every host-side edge call goes through: a
// --dry-run reports an unreachable channel instead of spawning the
// `erun open --reconnect` that re-establishes it. That spawn is real work which
// outlives the preview, so the assertion is that it was never invoked -- a
// process table read from inside the suite would race the child's own exit and
// cannot establish that the preview left nothing behind.
func TestCallMCPToolWithReattachDryRunNeverReattaches(t *testing.T) {
	seedDesktopIdentityForTest(t)

	var spawned []string
	restore := swapReattachMCPChannel(func(_ common.Context, tenant, environment string) error {
		spawned = append(spawned, tenant+"/"+environment)
		return nil
	})
	t.Cleanup(restore)

	port := closedLoopbackPort(t)
	target := mcpEdgeTarget{
		tenant:      "team",
		environment: "dev",
		port:        port,
		endpoint:    common.MCPLocalEndpoint(port),
	}

	_, err := callMCPToolWithReattach(context.Background(), testCommandContext(t, true), target, "whip", map[string]any{"preview": true}, false)
	if !errors.Is(err, common.ErrMCPEndpointUnreachable) {
		t.Fatalf("dry run against a closed port returned %v, want an unreachable endpoint", err)
	}
	if len(spawned) != 0 {
		t.Fatalf("--dry-run reattached %v, want no reattach at all", spawned)
	}

	// The guard must stop the preview only: a real run still recovers the
	// channel, which is the whole reason this choke point exists.
	_, _ = callMCPToolWithReattach(context.Background(), testCommandContext(t, false), target, "whip", map[string]any{}, false)
	if len(spawned) != 1 || spawned[0] != "team/dev" {
		t.Fatalf("a real run reattached %v, want exactly team/dev", spawned)
	}
}

func testCommandContext(t *testing.T, dryRun bool) common.Context {
	t.Helper()
	var out bytes.Buffer
	return common.Context{Logger: common.NewLoggerWithWriters(0, &out, &out), DryRun: dryRun}
}

func swapReattachMCPChannel(replacement func(common.Context, string, string) error) func() {
	original := reattachMCPChannel
	reattachMCPChannel = replacement
	return func() { reattachMCPChannel = original }
}

// closedLoopbackPort returns a loopback port with nothing listening on it, so
// the call fails as unreachable without depending on any external service.
func closedLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a loopback port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release loopback port %d: %v", port, err)
	}
	return port
}

// seedDesktopIdentityForTest writes an ed25519 identity where the CLI resolves
// it, so minting the call's bearer succeeds and the call reaches the dial
// rather than failing before the branch under test.
func seedDesktopIdentityForTest(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", os.Getenv("XDG_CONFIG_HOME"))

	dir := common.DefaultDesktopIdentityDir()
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
