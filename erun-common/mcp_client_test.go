package eruncommon

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

// TestPostOnceWaitsForAForwardThatComesUpAfterStartup is the regression test
// for the reconnect gap: an MCP client's very first request (its own
// initialize call) used to fail outright when the local port-forward wasn't
// up yet at session start, and most MCP clients never repeat that call later
// in the session, so the environment stayed unreachable for the whole session
// even after `erun open` brought the forward up moments later. postOnce's
// bounded wait on a session's first attempt (see
// awaitLocalMCPEndpointReachable) closes that window: this test starts the
// session against a port nothing holds yet, brings up a listener on that
// exact port shortly after the request is already in flight, and asserts the
// request succeeds instead of failing immediately.
func TestPostOnceWaitsForAForwardThatComesUpAfterStartup(t *testing.T) {
	port := reserveLoopbackPort(t)

	session, err := newMCPSession(MCPLocalEndpoint(port), func() (string, error) { return "test-token", nil }, "test", false, true)
	if err != nil {
		t.Fatalf("newMCPSession: %v", err)
	}
	// Shrink the bound so a real regression (no wait at all) fails this test
	// promptly rather than only within the production 20s bound.
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

	// mcpStatusError turns the 401 this handler answers with into
	// ErrMCPUnauthorized -- that is success for this test's purposes: it proves
	// the request reached the edge instead of failing on the earlier dial.
	if !errors.Is(err, ErrMCPUnauthorized) {
		t.Fatalf("expected the request to reach the now-up endpoint (unauthorized), got: %v", err)
	}
	if elapsed >= 5*time.Second {
		t.Fatalf("request took %s -- did not return as soon as the endpoint came up", elapsed)
	}
}

// TestPostOnceStillFailsPromptlyWhenNothingEverAnswers guards the other side:
// an environment that is genuinely not open must not hang forever -- it must
// still fail with the ordinary unreachable error, just after (at most) the
// bounded wait, and only on this one first attempt.
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

	// A second attempt on the same session must not wait again -- only the
	// first ever attempt gets the startup grace period.
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

// TestOpenMCPSessionDoesNotWaitForStartup guards the scoping: the typed
// CLI/desktop path (openMCPSession, behind CallMCPTool/ListMCPTools) already
// has its own active recovery -- reattaching the port-forward and retrying --
// so it must not opt into postOnce's passive wait, which would only make an
// ordinary "not open yet" call slower for no benefit. Only the stdio proxy
// (RunMCPStdioProxy) should ever wait.
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

// The two held-port faults reach a caller as different errors, because the
// remedy each one leads to is the opposite of the other's: a stale forward is
// re-established, while a target that has not come up yet is waited for, and
// re-opening the environment behind a working forward does nothing at all.
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
	// Callers gate their own recovery on the broad class, so the narrower one
	// has to stay inside it.
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
	port := listener.Addr().(*net.TCPAddr).Port
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

// mcpEdgeForgettingItsSession stands in for the edge after it has dropped a
// caller's session: it handshakes normally and hands out a session id, then
// answers the next tool call the second way a lost session appears on the
// wire -- HTTP 200 with the SDK's session-initialization guard as the
// JSON-RPC error, rather than a 404. Only a fresh handshake clears it, which
// is exactly what the client's recovery has to do about it.
//
// A stand-in rather than a real edge because the state being reproduced is one
// the edge reaches on a timer: the session it was holding has gone, and the
// caller has no way to tell that from the response alone.
type mcpEdgeForgettingItsSession struct {
	handshakes int
	calls      int
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
		// The first call after the handshake is the one the edge no longer
		// has a session for; once the caller has handshaked again, the same
		// call is answered normally.
		if e.handshakes < 2 {
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

// TestTheClientRecoversWhenTheEdgeAnswersWithTheInitializationGuard is the
// reported failure: a caller whose session the edge has dropped gets HTTP 200
// with a JSON-RPC guard error, and the client -- which only ever re-handshaked
// on a 404 -- surfaced that as the tool failing, permanently, for a session a
// re-handshake would have replaced in one round trip.
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
