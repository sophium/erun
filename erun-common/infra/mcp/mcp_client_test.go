package mcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

// A client's initialize is never repeated, so the first request must wait for a forward
// that comes up moments after the session starts.
func TestPostOnceWaitsForAForwardThatComesUpAfterStartup(t *testing.T) {
	port := reserveLoopbackPort(t)

	session, err := newMCPSession(MCPLocalEndpoint(port), func() (string, error) { return "test-token", nil }, "test", false, true)
	if err != nil {
		t.Fatalf("newMCPSession: %v", err)
	}
	session.startupWait = 5 * time.Second

	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(300 * time.Millisecond)
		serveOnLoopbackPort(t, port, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})
	}()
	t.Cleanup(func() { <-done })

	begin := time.Now()
	_, err = session.postOnce(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	elapsed := time.Since(begin)

	// A 401 proves the request reached the edge rather than failing to dial.
	if !errors.Is(err, ErrMCPUnauthorized) {
		t.Fatalf("expected the request to reach the now-up endpoint (unauthorized), got: %v", err)
	}
	if elapsed >= 5*time.Second {
		t.Fatalf("request took %s -- did not return as soon as the endpoint came up", elapsed)
	}
}

func TestPostOnceStillFailsPromptlyWhenNothingEverAnswers(t *testing.T) {
	port := reserveLoopbackPort(t)

	session, err := newMCPSession(MCPLocalEndpoint(port), func() (string, error) { return "test-token", nil }, "test", false, true)
	if err != nil {
		t.Fatalf("newMCPSession: %v", err)
	}
	session.startupWait = 300 * time.Millisecond

	begin := time.Now()
	_, err = session.postOnce(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	elapsed := time.Since(begin)

	if err == nil {
		t.Fatal("expected an unreachable error when nothing ever answers")
	}
	if elapsed < 300*time.Millisecond {
		t.Fatalf("request failed after only %s -- the wait should still have run once", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("request took %s -- the bounded wait did not bound anything", elapsed)
	}

	begin = time.Now()
	_, err = session.postOnce(context.Background(), []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	elapsed = time.Since(begin)
	if err == nil {
		t.Fatal("expected the second attempt to also fail (nothing is listening)")
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("second attempt took %s -- it should not have waited again", elapsed)
	}
}

// Typed callers reattach and retry themselves; only the stdio proxy waits.
func TestOpenMCPSessionDoesNotWaitForStartup(t *testing.T) {
	port := reserveLoopbackPort(t)

	begin := time.Now()
	session, err := openMCPSession(context.Background(), MCPLocalEndpoint(port), func() (string, error) { return "test-token", nil }, "test", false)
	elapsed := time.Since(begin)

	if err == nil {
		t.Fatal("expected the handshake to fail against a port nothing is listening on")
	}
	if !errors.Is(err, ErrMCPEndpointUnreachable) {
		t.Fatalf("expected an unreachable error, got: %v", err)
	}
	if session != nil {
		t.Fatalf("expected no session on handshake failure, got %+v", session)
	}
	if elapsed > time.Second {
		t.Fatalf("handshake took %s -- the typed CLI/desktop path must fail fast, not wait for startup", elapsed)
	}
}

func reserveLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback port: %v", err)
	}
	port := loopbackListenerPort(t, listener)
	if err := listener.Close(); err != nil {
		t.Fatalf("release reserved port: %v", err)
	}
	return port
}

func serveOnLoopbackPort(t *testing.T, port int, handler http.HandlerFunc) {
	t.Helper()
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listen on reserved port %d: %v", port, err)
	}
	server := &http.Server{Handler: handler}
	t.Cleanup(func() { _ = server.Close() })
	go func() {
		_ = server.Serve(listener)
	}()
	waitForLocalPortReachable(t, port)
}

func waitForLocalPortReachable(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if CanReachLocalMCPEndpoint(port) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server on port %d never became reachable", port)
}

func loopbackListenerPort(t *testing.T, listener net.Listener) int {
	t.Helper()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %v is not TCP", listener.Addr())
	}
	return addr.Port
}
