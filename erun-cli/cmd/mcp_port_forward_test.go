package cmd

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// kubectlMCPPortForwardArgs is exercised end-to-end through the
// `open/remote_dry_run_traces_port_forwards` integration scenario, which
// asserts the trace shows the resolved kubectl port-forward command line.
// The cases below stay as unit tests because they verify a real network
// probe (canReachLocalMCPEndpoint) and log-file classification
// (mcpPortForwardTimeoutDetail) — neither is reachable from a --dry-run
// scenario without a stub.

func TestCanReachLocalMCPEndpointRequiresHTTPResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Bad Request", http.StatusBadRequest)
	}))
	defer server.Close()

	host, portValue, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split address: %v", err)
	}
	if host == "" {
		t.Fatal("expected listener host")
	}
	port, err := strconv.Atoi(portValue)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	if !canReachLocalMCPEndpoint(port) {
		t.Fatal("expected HTTP endpoint to be reachable")
	}
}

func TestMCPPortForwardTimeoutDetailClassifiesRecentLog(t *testing.T) {
	tests := []struct {
		name string
		log  string
		want string
	}{
		{
			name: "connection refused",
			log:  `failed to connect to localhost:17400, IPv4: dial tcp4 127.0.0.1:17400: connect: connection refused`,
			want: "runtime pod exists but MCP is not accepting connections yet",
		},
		{
			name: "lost connection",
			log:  "error: lost connection to pod",
			want: "runtime pod connection was lost, likely because the pod restarted",
		},
		{
			name: "pod not found",
			log:  `error: error upgrading connection: unable to upgrade connection: pod not found ("petios-devops-123")`,
			want: "runtime pod was replaced while connecting",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.log")
			if err := os.WriteFile(path, []byte(tt.log), 0o644); err != nil {
				t.Fatalf("write log: %v", err)
			}
			if got := mcpPortForwardTimeoutDetail(path); got != tt.want {
				t.Fatalf("unexpected detail:\ngot:  %q\nwant: %q", got, tt.want)
			}
		})
	}
}
