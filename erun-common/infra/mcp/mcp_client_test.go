package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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

// A stale forward is re-established, while an unready target is waited for.
func TestCallMCPToolNamesAnUnreadyTargetRatherThanAStaleForward(t *testing.T) {
	port := startDroppingListener(t)

	_, err := CallMCPTool(context.Background(), MCPToolCallParams{
		Endpoint:      MCPLocalEndpoint(port),
		MintToken:     func() (string, error) { return "test-token", nil },
		ClientVersion: "test",
		Tool:          "version",
	})
	if err == nil {
		t.Fatal("expected the call to fail against a forward whose target never answered")
	}
	if !errors.Is(err, ErrMCPTargetNotAnswering) {
		t.Fatalf("expected an unready target, got: %v", err)
	}
	// Callers gate recovery on the broad class, so the narrower error must wrap it.
	if !errors.Is(err, ErrMCPEndpointUnreachable) {
		t.Fatalf("an unready target is still an unreachable endpoint, got: %v", err)
	}
	if strings.Contains(err.Error(), "stale port-forward") {
		t.Fatalf("an unready target must not be reported as a stale forward, got: %v", err)
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

// mcpEdgeForgettingItsSession answers the call after a handshake with HTTP 200 and the
// session-initialization error, until the caller handshakes again.
type mcpEdgeForgettingItsSession struct {
	handshakes int
	calls      int
	// refuseEverySession answers the guard however often the caller handshakes.
	refuseEverySession bool
}

func (e *mcpEdgeForgettingItsSession) serve(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	method := ""
	var decoded struct {
		ID     int    `json:"id"`
		Method string `json:"method"`
	}
	if err := json.Unmarshal(body, &decoded); err == nil {
		method = decoded.Method
	}
	switch method {
	case "initialize":
		e.handshakes++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", fmt.Sprintf("session-%d", e.handshakes))
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"erun","version":"1.2.3"}}}`, decoded.ID)
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		if e.refuseEverySession || e.handshakes < 2 {
			e.calls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32603,"message":"method \"tools/list\" is invalid during session initialization"}}`, decoded.ID)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"version","description":"Return build metadata","inputSchema":{"type":"object"}}]}}`, decoded.ID)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}

func TestTheClientRecoversWhenTheEdgeAnswersWithTheInitializationGuard(t *testing.T) {
	edge := &mcpEdgeForgettingItsSession{}
	server := httptest.NewServer(http.HandlerFunc(edge.serve))
	t.Cleanup(server.Close)

	params := MCPToolListParams{
		Endpoint:      server.URL,
		MintToken:     func() (string, error) { return "test-token", nil },
		ClientVersion: "test",
	}
	listed, err := ListMCPTools(context.Background(), params)
	if err != nil {
		t.Fatalf("a session the edge no longer holds must be re-handshaked and retried, not reported as a tool failure: %v", err)
	}
	if len(listed.Tools) != 1 || listed.Tools[0].Name != "version" {
		t.Fatalf("expected the retried call to return the edge's tools, got %+v", listed.Tools)
	}
	if edge.handshakes != 2 {
		t.Fatalf("expected exactly one recovery handshake, saw %d", edge.handshakes)
	}
	if edge.calls != 1 {
		t.Fatalf("expected the guard to be answered once, saw it %d times", edge.calls)
	}
}

// An edge that answers but never accepts a session is a session loss, not an unreachable endpoint.
func TestALiveEdgeThatRefusesEverySessionIsNotReportedAsAnUnreachableEndpoint(t *testing.T) {
	edge := &mcpEdgeForgettingItsSession{refuseEverySession: true}
	server := httptest.NewServer(http.HandlerFunc(edge.serve))
	t.Cleanup(server.Close)

	_, err := ListMCPTools(context.Background(), MCPToolListParams{
		Endpoint:      server.URL,
		MintToken:     func() (string, error) { return "test-token", nil },
		ClientVersion: "test",
	})
	if err == nil {
		t.Fatal("expected a call no session is ever accepted for to fail")
	}
	if !errors.Is(err, ErrMCPSessionLost) {
		t.Fatalf("a live edge that will not hold a session is a session loss, got: %v", err)
	}
	if errors.Is(err, ErrMCPEndpointUnreachable) {
		t.Fatalf("an edge that answered must not be reported as unreachable, got: %v", err)
	}
	if !strings.Contains(err.Error(), "answered") {
		t.Fatalf("the failure has to say the edge answered, got: %v", err)
	}
	if edge.handshakes != 2 {
		t.Fatalf("expected exactly one recovery handshake, saw %d", edge.handshakes)
	}
}
