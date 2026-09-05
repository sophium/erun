package eruncommon

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// A listener that accepts and then never answers is exactly what a stale
// kubectl port-forward is, and it is the case a dial-based check cannot see:
// the handshake succeeds, so the tunnel reads as healthy while every call
// through it hangs. This is the whole reason reachability is a round trip.
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
			// Held open and never answered, like the stale forward.
			defer func() { _ = conn.Close() }()
		}
	}()

	port := listener.Addr().(*net.TCPAddr).Port

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

// Any answer proves the edge replied — an unauthenticated request is answered
// 401, and that is enough. Requiring a success status would report a healthy
// tunnel as broken.
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

// ClassifyLocalMCPUnreachable is what DescribeLocalMCPUnreachable's own branch
// reduces to; pinning it directly means a caller that needs the kind on its
// own (to pick a UI treatment, say) does not have to parse the sentence for it.
func TestClassifyLocalMCPUnreachableNamesTheKind(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	boundPort := listener.Addr().(*net.TCPAddr).Port
	defer func() { _ = listener.Close() }()

	freeListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	freePort := freeListener.Addr().(*net.TCPAddr).Port
	_ = freeListener.Close()

	if got := ClassifyLocalMCPUnreachable(boundPort); got != LocalMCPStaleForward {
		t.Fatalf("a held port must classify as stale-forward, got %q", got)
	}
	if got := ClassifyLocalMCPUnreachable(freePort); got != LocalMCPNotOpen {
		t.Fatalf("a free port must classify as not-open, got %q", got)
	}
}

// Nothing listening is a different problem from a forward that has gone stale,
// and the operator's next move differs, so the two must not share wording.
func TestDescribeLocalMCPUnreachableSeparatesMissingFromStale(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	detail := DescribeLocalMCPUnreachable("acme", "dev", port)
	if !strings.Contains(detail, "no port-forward is listening") {
		t.Fatalf("a missing forward must say so, got %q", detail)
	}
	if strings.Contains(detail, "not carrying traffic") {
		t.Fatalf("a missing forward is not a stale one, got %q", detail)
	}
}
