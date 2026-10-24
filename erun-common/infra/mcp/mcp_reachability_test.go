package mcp

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// A stale port-forward accepts and never answers, which a dial-based check cannot see.
func TestCanReachLocalMCPEndpointSeesThroughASilentListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			defer func() { _ = conn.Close() }()
		}
	}()

	port := loopbackListenerPort(t, listener)

	if !LocalPortIsBound(port) {
		t.Fatal("the dial must succeed — that is the trap this exists for")
	}
	if CanReachLocalMCPEndpoint(port) {
		t.Fatal("a listener that never answers must not read as reachable")
	}
	if detail := DescribeLocalMCPUnreachable("acme", "dev", port); !strings.Contains(detail, "not carrying traffic") {
		t.Fatalf("a stale forward must be named as such, got %q", detail)
	}
}

// A 401 still proves the edge replied.
func TestCanReachLocalMCPEndpointAcceptsAnyAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	_, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatalf("split test server address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse test server port: %v", err)
	}

	if !CanReachLocalMCPEndpoint(port) {
		t.Fatal("a 401 already proves the edge answered")
	}
}

func TestClassifyLocalMCPUnreachableNamesTheKind(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	boundPort := loopbackListenerPort(t, listener)
	defer func() { _ = listener.Close() }()

	freeListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	freePort := loopbackListenerPort(t, freeListener)
	_ = freeListener.Close()

	if got := ClassifyLocalMCPUnreachable(boundPort); got != LocalMCPStaleForward {
		t.Fatalf("a held port must classify as stale-forward, got %q", got)
	}
	if got := ClassifyLocalMCPUnreachable(freePort); got != LocalMCPNotOpen {
		t.Fatalf("a free port must classify as not-open, got %q", got)
	}
}

// startHeldOpenListener accepts and never answers, like a stale forward.
func startHeldOpenListener(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			defer func() { _ = conn.Close() }()
		}
	}()
	return loopbackListenerPort(t, listener)
}

// startDroppingListener accepts and closes without replying, like a forward whose target is not up.
func startDroppingListener(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	return loopbackListenerPort(t, listener)
}

func TestClassifyLocalMCPUnreachableSeparatesAnUnreadyTargetFromAStaleForward(t *testing.T) {
	unreadyPort := startDroppingListener(t)
	stalePort := startHeldOpenListener(t)

	if got := ClassifyLocalMCPUnreachable(unreadyPort); got != LocalMCPTargetNotAnswering {
		t.Fatalf("a forward that took the connection and closed it must classify as target-not-answering, got %q", got)
	}
	if got := ClassifyLocalMCPUnreachable(stalePort); got != LocalMCPStaleForward {
		t.Fatalf("a forward that accepts and never answers must still classify as stale-forward, got %q", got)
	}

	unready := DescribeLocalMCPUnreachable("acme", "dev", unreadyPort)
	if strings.Contains(unready, "not carrying traffic") {
		t.Fatalf("an unready target is not a forward that has stopped carrying traffic, got %q", unready)
	}
	if !strings.Contains(unready, "still starting") {
		t.Fatalf("an unready target must be named as one, got %q", unready)
	}
	if stale := DescribeLocalMCPUnreachable("acme", "dev", stalePort); !strings.Contains(stale, "not carrying traffic") {
		t.Fatalf("a stale forward must still be named as one, got %q", stale)
	}
}

func TestDescribeLocalMCPUnreachableSeparatesMissingFromStale(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := loopbackListenerPort(t, listener)
	_ = listener.Close()

	detail := DescribeLocalMCPUnreachable("acme", "dev", port)
	if !strings.Contains(detail, "no port-forward is listening") {
		t.Fatalf("a missing forward must say so, got %q", detail)
	}
	if strings.Contains(detail, "not carrying traffic") {
		t.Fatalf("a missing forward is not a stale one, got %q", detail)
	}
}

func loopbackListenerPort(t *testing.T, listener net.Listener) int {
	t.Helper()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %v is not TCP", listener.Addr())
	}
	return addr.Port
}
